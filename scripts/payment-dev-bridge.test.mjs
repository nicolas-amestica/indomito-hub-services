import test from 'node:test';
import assert from 'node:assert/strict';
import { request } from 'node:http';
import { createBridge } from './payment-dev-bridge.mjs';

test('puente local: rutas, origen, idempotencia y errores redactados', async t => {
  const calls = [];
  const server = createBridge(async (...args) => {
    calls.push(args);
    if (args[0].endsWith('/verificar')) throw new Error('SECRET_MUST_NOT_LEAK');
    return { status: 200, body: { data: { mode: 'khipu-development' } } };
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));
  const id = '01K00000000000000000000000';
  const call = (path, method = 'GET', extra = {}, body = '') => new Promise((resolve, reject) => {
    const req = request({ host: '127.0.0.1', port: server.address().port, path, method,
      headers: { Host: '127.0.0.1:8096', 'X-Payment-Dev-Client': 'local', ...extra } }, res => {
      let data = '';
      res.on('data', chunk => { data += chunk; });
      res.on('end', () => resolve({ status: res.statusCode, body: data, headers: res.headers }));
    });
    req.on('error', reject);
    req.end(body);
  });
  const postHeaders = { Origin: 'http://localhost:4400', 'Content-Type': 'application/json', 'Idempotency-Key': id };
  assert.equal((await call('/api/khipu-dev/configuracion')).status, 200);
  assert.equal((await call('/api/khipu-dev/configuracion', 'GET', { Origin: 'https://evil.example' })).status, 403);
  assert.equal((await call('/api/khipu-dev/configuracion', 'GET', { Host: 'evil.example' })).status, 403);
  assert.equal((await call('/api/khipu-dev/configuracion', 'GET', { 'X-Payment-Dev-Client': '' })).status, 403);
  assert.equal((await call('/api/khipu-dev/configuracion?url=https://evil.example')).status, 404);
  assert.equal((await call('/api/khipu-dev/khipu/notificaciones', 'POST', postHeaders, '{}')).status, 404);
  assert.equal((await call('/api/khipu-dev/pruebas', 'GET')).status, 405);
  assert.equal((await call('/api/khipu-dev/pruebas', 'POST', { 'Content-Type': 'application/json' }, '{}')).status, 403);
  assert.equal((await call('/api/khipu-dev/pruebas', 'POST', { ...postHeaders, 'Idempotency-Key': 'bad' }, '{}')).status, 400);
  assert.equal((await call('/api/khipu-dev/pruebas', 'POST', postHeaders, '{"amount":1}')).status, 400);
  assert.equal((await call('/api/khipu-dev/pruebas', 'POST', postHeaders, 'x'.repeat(1025))).status, 413);
  assert.equal((await call('/api/khipu-dev/pruebas', 'POST', postHeaders, '{}')).status, 200);
  assert.deepEqual(calls[1], ['/pagos/pruebas', 'POST', id]);
  const failure = await call(`/api/khipu-dev/pruebas/${id}/verificar`, 'POST', postHeaders, '{}');
  assert.equal(failure.status, 502);
  assert.equal(failure.body.includes('SECRET'), false);
  assert.equal(failure.headers['access-control-allow-origin'], undefined);
  assert.equal(failure.headers['cache-control'], 'no-store');
  assert.equal(calls.length, 3);
});
