const http = require('node:http');

const port = Number(process.env.PORT || 4517);
const uploads = new Map();
const sends = new Map();
const attempts = new Map();
const allowedMIMEs = new Set([
  'application/pdf', 'image/jpeg', 'image/png', 'image/gif', 'image/webp',
  'audio/ogg', 'audio/aac', 'audio/mp4', 'audio/mpeg', 'audio/amr', 'video/mp4',
]);

const json = (response, status, body) => {
  response.writeHead(status, { 'content-type': 'application/json' });
  response.end(JSON.stringify(body));
};

const readJSON = async (request) => {
  if (request.body !== undefined) {
    if (Buffer.isBuffer(request.body)) return JSON.parse(request.body.toString('utf8') || '{}');
    if (typeof request.body === 'string') return JSON.parse(request.body || '{}');
    return request.body;
  }
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  return JSON.parse(Buffer.concat(chunks).toString('utf8') || '{}');
};

const header = (request, name) => {
  const value = request.headers?.[name.toLowerCase()];
  return Array.isArray(value) ? value[0] : value;
};

const sendState = (clientMessageId, phone, attachment = null) => ({
  data: {
    clientMessageId,
    phone,
    state: 'committed',
    createdAt: '2026-10-24T12:00:00Z',
    updatedAt: '2026-10-24T12:00:01Z',
    retryCount: 0,
    preview: 'P4 local harness',
    attachment,
    whatsappMessageId: `wamid.${clientMessageId}`,
  },
});

const server = http.createServer(async (request, response) => {
  try {
    const requestOrigin = `http://${request.headers.host || `127.0.0.1:${port}`}`;
    const url = new URL(request.url || '/', requestOrigin);
    const path = url.pathname;
    const phoneMatch = path.match(/^\/conversaciones\/([0-9]{8,15})(?:\/(.*))?$/);

    if (request.method === 'PUT' && path.startsWith('/upload/')) {
      for await (const _chunk of request) { /* consume upload */ }
      const uploadId = path.slice('/upload/'.length);
      const upload = uploads.get(uploadId);
      if (!upload) return json(response, 404, { code: 'INBOX_UPLOAD_NOT_FOUND' });
      if (url.searchParams.get('scenario') === 'expired') {
        return json(response, 410, { code: 'INBOX_UPLOAD_EXPIRED' });
      }
      if (header(request, 'x-amz-checksum-sha256') !== upload.checksum) {
        return json(response, 422, { code: 'INBOX_CHECKSUM_MISMATCH' });
      }
      upload.uploaded = true;
      response.writeHead(200);
      return response.end();
    }
    if (header(request, 'authorization') !== 'Bearer local-test-token') {
      return json(response, 401, { code: 'UNAUTHORIZED' });
    }
    if (request.method === 'GET' && path === '/conversaciones') {
      return json(response, 200, { data: [], pageInfo: { page: 1, pageSize: 20, totalItems: 0, totalPages: 0 } });
    }
    if (!phoneMatch) return json(response, 501, { code: 'MOCK_ENDPOINT_NOT_IMPLEMENTED' });

    const phone = phoneMatch[1];
    const operation = phoneMatch[2] || '';
    if (request.method === 'GET' && operation === '') {
      return json(response, 200, { data: { mode: 'full', messages: [{ id: '01K8P4MOCK0000000000000000' }] } });
    }

    let body;
    try {
      body = await readJSON(request);
    } catch (error) {
      if (error instanceof SyntaxError) return json(response, 400, { code: 'INVALID_JSON' });
      throw error;
    }
    if (request.method === 'POST' && operation === 'adjuntos/preparar') {
      const file = body.files?.[0];
      if (!file) return json(response, 400, { code: 'INBOX_INVALID_ATTACHMENT' });
      if (!allowedMIMEs.has(file.mimeType)) return json(response, 415, { code: 'INBOX_UNSUPPORTED_MEDIA_TYPE' });
      if (file.size > 7 * 1024 * 1024) return json(response, 413, { code: 'INBOX_ATTACHMENT_TOO_LARGE' });
      const uploadId = body.batchId;
      const scenario = header(request, 'x-p4-scenario');
      const upload = {
        clientMessageId: file.clientMessageId,
        fileName: file.fileName,
        mimeType: file.mimeType,
        size: file.size,
        checksum: file.checksumSha256,
        uploaded: false,
      };
      uploads.set(uploadId, upload);
      uploads.set(file.clientMessageId, upload);
      return json(response, 200, {
        data: {
          batchId: body.batchId,
          expiresAt: '2026-10-24T12:15:00Z',
          uploads: [{
            clientMessageId: file.clientMessageId,
            uploadId,
            key: `mock/${file.fileName}`,
            uploadUrl: `${requestOrigin}/upload/${uploadId}${scenario === 'expired-upload' ? '?scenario=expired' : ''}`,
            method: 'PUT',
            requiredHeaders: {
              'Content-Type': file.mimeType,
              'x-amz-checksum-sha256': file.checksumSha256,
              'x-amz-meta-upload-id': uploadId,
            },
          }],
        },
      });
    }
    if (request.method === 'POST' && operation === 'adjuntos') {
      const upload = uploads.get(body.clientMessageId);
      if (!upload?.uploaded) return json(response, 409, { code: 'INBOX_UPLOAD_NOT_COMPLETED' });
      const existing = sends.get(body.clientMessageId);
      if (existing) return json(response, 200, { ...existing, deduplicated: true });
      const state = sendState(body.clientMessageId, phone, upload);
      sends.set(body.clientMessageId, state);
      return json(response, 200, state);
    }
    if (request.method === 'PUT' && operation === 'reacciones') {
      return json(response, 200, { data: { messageId: body.messageId, emoji: body.emoji } });
    }
    if (request.method === 'POST' && ['respuestas', 'plantillas', 'mensajes-enriquecidos'].includes(operation)) {
      const scenario = header(request, 'x-p4-scenario');
      if (scenario === 'transient-meta') {
        const count = (attempts.get(body.clientMessageId) || 0) + 1;
        attempts.set(body.clientMessageId, count);
        if (count < 3) return json(response, 503, { code: 'META_TEMPORARILY_UNAVAILABLE', retryable: true, attempt: count });
      }
      if (scenario === 'permanent-meta') {
        return json(response, 502, { code: 'META_REQUEST_REJECTED', retryable: false });
      }
      const existing = sends.get(body.clientMessageId);
      if (existing) return json(response, 200, { ...existing, deduplicated: true });
      const state = sendState(body.clientMessageId, phone);
      state.data.retryCount = attempts.get(body.clientMessageId) || 0;
      sends.set(body.clientMessageId, state);
      return json(response, 200, state);
    }
    return json(response, 501, { code: 'MOCK_ENDPOINT_NOT_IMPLEMENTED' });
  } catch (error) {
    return json(response, 500, { code: 'MOCK_FAILURE', message: error.message });
  }
});

server.listen(port, '127.0.0.1');
