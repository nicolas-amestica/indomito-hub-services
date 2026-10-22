import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const config = require('../serverless.ts');

test('migración Go mantiene nombres y artefactos individuales', () => {
  assert.equal(config.service, 'payment-monitoring');
  assert.equal(config.provider.runtime, 'provided.al2023');
  assert.equal(config.provider.architecture, 'arm64');
  assert.deepEqual(Object.keys(config.functions).sort(), ['alarm', 'monitor']);
  for (const name of ['monitor', 'alarm']) {
    assert.equal(config.functions[name].handler, 'bootstrap');
    assert.equal(config.functions[name].package.artifact, `.serverless-artifacts/${name}.zip`);
  }
});

test('monitoreo y alarmas sin permisos de escritura financiera ni Scan', () => {
  const statements = config.provider.iam.role.statements;
  assert.ok(!JSON.stringify(statements).includes('dynamodb:Scan'));
  const writes = statements.filter((s: { Action: string[] }) => s.Action.some(a => a.startsWith('dynamodb:') && !['dynamodb:GetItem', 'dynamodb:Query'].includes(a)));
  assert.equal(writes.length, 1);
  assert.deepEqual(writes[0].Condition['ForAllValues:StringEquals']['dynamodb:LeadingKeys'], ['RENDITION_MONITOR']);
  assert.deepEqual(config.functions.monitor.events, [{ schedule: 'rate(15 minutes)' }]);
  assert.ok(config.functions.alarm.onError);
  assert.ok(config.provider.iam.role.statements.some((s: { Action: string[] }) => s.Action.includes('sqs:SendMessage')));
});
test('incluye fallos HTTP, trabajador detenido y monitor detenido, sin bucle SMTP', () => {
  const r = config.resources.Resources;
  for (const name of ['ReceiverErrors', 'ReceiverThrottles', 'WorkerErrors', 'WorkerThrottles', 'ReceiverRejected', 'MonitorErrors']) {
    assert.equal(r[name].Properties.TreatMissingData, 'notBreaching');
    assert.ok(r[name].Properties.AlarmActions.length);
    assert.ok(r[name].Properties.OKActions.length);
  }
  for (const name of ['WorkerNotRunning', 'MonitorNotHealthy']) assert.equal(r[name].Properties.TreatMissingData, 'breaching');
  assert.equal(r.EmailDeliveryErrors.Properties.AlarmActions, undefined);
  assert.equal(r.EmailDeadLetters.Properties.AlarmActions, undefined);
  assert.ok(r.AlertTopicPolicy.Properties.PolicyDocument.Statement[0].Condition.StringEquals['aws:SourceAccount']);
});
