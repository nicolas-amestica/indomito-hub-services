/** Herramienta exclusivamente local. Nunca desplegar ni publicar este puerto. */
import { execFile } from 'node:child_process';
import { createHmac } from 'node:crypto';
import { createServer } from 'node:http';
import { promisify } from 'node:util';
import { pathToFileURL } from 'node:url';

const execute = promisify(execFile);
const idPattern = '[0-7][0-9A-HJKMNP-TV-Z]{25}';
const routePattern = new RegExp(`^/api/khipu-dev/(configuracion|pruebas(?:/${idPattern}(?:/verificar)?)?)$`);

async function aws(args) {
  const { stdout } = await execute('aws', [...args, '--profile', 'pa-dev', '--region', 'us-east-1', '--output', 'json'], { timeout: 20000, maxBuffer: 262144 });
  return JSON.parse(stdout);
}

export async function developmentClient() {
  if ((await aws(['sts', 'get-caller-identity'])).Account !== '382670112717') throw new Error('Cuenta no DEV');
  const stack = await aws(['cloudformation', 'describe-stacks', '--stack-name', 'indomito-hub-infra-api-gateway-dev']);
  const base = stack.Stacks[0].Outputs.find(item => item.OutputKey === 'HttpApiUrl')?.OutputValue;
  if (!/^https:\/\/[a-z0-9]+\.execute-api\.us-east-1\.amazonaws\.com$/.test(base ?? '')) throw new Error('Gateway inválido');
  let cached;
  let refresh;
  async function signingSecret() {
    if (cached && cached.until > Date.now()) return cached.value;
    refresh ??= aws(['ssm', 'get-parameter', '--name', '/indomito/dev/auth/jwt-secret', '--with-decryption']).then(result => {
      if (result.Parameter.Type !== 'SecureString' || !result.Parameter.Value) throw new Error('SSM inválido');
      cached = { value: result.Parameter.Value, until: Date.now() + 240000 };
      return cached.value;
    }).finally(() => { refresh = undefined; });
    return refresh;
  }
  await signingSecret();
  return async (path, method, key) => {
    const secret = await signingSecret();
    const now = Math.floor(Date.now() / 1000);
    const encode = value => Buffer.from(JSON.stringify(value)).toString('base64url');
    const unsigned = `${encode({ alg: 'HS256', typ: 'JWT' })}.${encode({ iat: now, exp: now + 60, body: { userId: 'payment-dev-cli', access: [{ endpoint: '/pagos', allowances: method === 'POST' ? 'cr' : 'r' }] } })}`;
    const token = `${unsigned}.${createHmac('sha256', secret).update(unsigned).digest('base64url')}`;
    const response = await fetch(`${base}${path}`, {
      method, redirect: 'error', signal: AbortSignal.timeout(35000),
      headers: { Authorization: `Bearer ${token}`, ...(method === 'POST' && { 'Content-Type': 'application/json' }), ...(key && { 'Idempotency-Key': key }) },
      ...(method === 'POST' && { body: '{}' }),
    });
    if (!response.ok) return { status: response.status, body: { error: 'No se pudo completar la operación DEV. Conserva el ID del intento.' } };
    const body = await response.json();
    return { status: response.status, body };
  };
}

export function createBridge(call) {
  let active = 0;
  let windowStart = Date.now();
  let requests = 0;
  const server = createServer(async (req, res) => {
    const send = (status, body) => {
      res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff' });
      res.end(JSON.stringify(body));
    };
    const fail = status => send(status, { error: 'Solicitud DEV no disponible.' });
    const origin = req.headers.origin;
    if (!['127.0.0.1', '::1'].includes(req.socket.remoteAddress) ||
        !['127.0.0.1:8096', 'localhost:8096'].includes(req.headers.host) ||
        req.headers['x-payment-dev-client'] !== 'local' ||
        (origin && origin !== 'http://localhost:4400') ||
        req.headers['sec-fetch-site'] === 'cross-site') return fail(403);
    const match = routePattern.exec(req.url ?? '');
    if (!match) return fail(404);
    const route = match[1];
    const post = route === 'pruebas' || route.endsWith('/verificar');
    if (req.method !== (post ? 'POST' : 'GET')) return fail(405);
    if (post && (origin !== 'http://localhost:4400' || req.headers['content-type'] !== 'application/json')) return fail(403);
    const key = req.headers['idempotency-key'];
    if (route === 'pruebas' && (typeof key !== 'string' || !new RegExp(`^${idPattern}$`).test(key))) return fail(400);
    if (Date.now() - windowStart >= 60000) { windowStart = Date.now(); requests = 0; }
    if (++requests > 60 || active >= 5) return fail(429);
    active++;
    try {
      let bytes = 0;
      const chunks = [];
      for await (const chunk of req) {
        bytes += chunk.length;
        if (bytes > 1024) { fail(413); return; }
        chunks.push(chunk);
      }
      const body = Buffer.concat(chunks).toString('utf8');
      if ((post && body.trim() !== '{}') || (!post && bytes > 0)) return fail(400);
      const result = await call(`/pagos/${route}`, req.method, key);
      send(result.status, result.body);
    } catch { fail(502); }
    finally { active--; }
  });
  server.requestTimeout = 10000;
  server.headersTimeout = 10000;
  server.maxHeadersCount = 30;
  return server;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const server = createBridge(await developmentClient());
    server.on('error', () => { console.error('No se pudo abrir el puerto local 8096.'); process.exitCode = 1; });
    server.listen(8096, '127.0.0.1', () => console.log('Puente Khipu DEV listo en 127.0.0.1:8096. Portal: http://localhost:4400/pruebas-khipu. Sin crear pagos.'));
  } catch {
    console.error('No se pudo iniciar. Verifica la sesión AWS pa-dev y los permisos SSM. No se imprimieron credenciales.');
    process.exitCode = 1;
  }
}
