import test from 'node:test';
import assert from 'node:assert/strict';

import { collectEvidence } from './collect-api-inbox-p4-evidence.mjs';

const valid = () => ({
  runId: 'dev-20261024-120000',
  stage: 'dev',
  records: [{
    capability: 'text',
    clientMessageId: '11111111-1111-4111-8111-111111111111',
    wamid: 'wamid.real-value',
    persistence: { clientMessageId: '11111111-1111-4111-8111-111111111111', state: 'committed' },
    realtime: { clientMessageId: '11111111-1111-4111-8111-111111111111', eventType: 'conversation.updated' },
    metaStatuses: ['sent'],
  }],
});

test('genera evidencia correlacionada sin identificadores originales', () => {
  const report = collectEvidence(valid());
  assert.deepEqual(report.summary, { total: 1, correlated: 1, failed: 0 });
  const serialized = JSON.stringify(report);
  assert.doesNotMatch(serialized, /11111111-1111-4111-8111-111111111111|wamid\.real-value/);
});

test('rechaza secretos y datos personales por nombre de campo', () => {
  const input = valid();
  input.records[0].accessToken = 'secret';
  assert.throws(() => collectEvidence(input), /campo sensible no permitido/);
});

test('rechaza evidencia sin correlacion completa', () => {
  const input = valid();
  input.records[0].realtime.clientMessageId = '22222222-2222-4222-8222-222222222222';
  assert.throws(() => collectEvidence(input), /no correlaciona realtime/);
});
