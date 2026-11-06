const assert = require('node:assert/strict');
const test = require('node:test');
const payment = require('../services/api-payment/serverless.ts');
const monitoring = require('../services/payment-monitoring/serverless.ts');

test('el rol real del consumidor puede leer el trabajo durable', () => {
  const fn = payment.functions['fn-conciliar-intentos-automaticamente-v1'];
  const role = payment.resources.Resources[fn.role];
  const statements = role.Properties.Policies.flatMap(p => p.PolicyDocument.Statement);
  assert.ok(statements.some(s => s.Effect === 'Allow' && s.Action.includes('dynamodb:GetItem') &&
    s.Condition?.['ForAllValues:StringEquals']?.['dynamodb:LeadingKeys']?.includes('RECONCILIATION#PENDING')));
  assert.deepEqual(fn.events[0].sqs.functionResponseType, 'ReportBatchItemFailures');
});

test('la DLQ tiene una alarma PRD conectada al canal SMTP existente', () => {
  assert.ok(payment.resources.Outputs.PaymentReconciliationDLQName);
  const alarm = monitoring.resources.Resources.PaymentReconciliationDeadLetters;
  if (process.env.STAGE !== 'prd') return assert.equal(alarm, undefined);
  assert.equal(alarm.Properties.MetricName, 'ApproximateNumberOfMessagesVisible');
  assert.deepEqual(alarm.Properties.AlarmActions, [{ Ref: 'RenditionAlerts' }]);
  assert.equal(alarm.Properties.Dimensions[0].Value, '${cf:api-payment-prd.PaymentReconciliationDLQName}');
});

test('la creación permite guardar recuperación y la alerta usa el canal existente', () => {
  const fn = payment.functions['fn-crear-checkout-cuota-v1'];
  const role = payment.resources.Resources[fn.role];
  const statements = role.Properties.Policies.flatMap(p => p.PolicyDocument.Statement);
  assert.ok(statements.some(s => s.Effect === 'Allow' && s.Action.includes('dynamodb:PutItem') &&
    s.Condition?.['ForAllValues:StringLike']?.['dynamodb:LeadingKeys']?.includes('RECONCILIATION#PENDING')));
  const alarm = monitoring.resources.Resources.CheckoutRecoveryRequired;
  if (process.env.STAGE !== 'prd') return assert.equal(alarm, undefined);
  assert.equal(alarm.Properties.Namespace, 'Indomito/Payments');
  assert.equal(alarm.Properties.MetricName, 'CheckoutRecoveryRequired');
  assert.deepEqual(alarm.Properties.AlarmActions, [{ Ref: 'RenditionAlerts' }]);
});

test('errores de Khipu tienen alerta PRD sin nuevos permisos de escritura financiera', () => {
  const alarm = monitoring.resources.Resources.ProviderRecoveryRequired;
  if (process.env.STAGE !== 'prd') return assert.equal(alarm, undefined);
  assert.equal(alarm.Properties.Namespace, 'Indomito/Payments');
  assert.equal(alarm.Properties.MetricName, 'ProviderRecoveryRequired');
  assert.deepEqual(alarm.Properties.Dimensions, [{ Name: 'Stage', Value: 'prd' }]);
  assert.deepEqual(alarm.Properties.AlarmActions, [{ Ref: 'RenditionAlerts' }]);
});

test('el recuperador usa intervalos y permisos acotados sin recursos de alerta nuevos', () => {
  const fn = payment.functions['fn-recuperar-conciliaciones-v1'];
  assert.equal(fn.memorySize, 128);
  assert.equal(fn.timeout, 29);
  assert.deepEqual(fn.events, [{ schedule: 'rate(15 minutes)' }]);
  const role = payment.resources.Resources[fn.role];
  const statements = role.Properties.Policies.flatMap(p => p.PolicyDocument.Statement);
  const ddb = statements.filter(s => s.Action.some(a => a.startsWith('dynamodb:')));
  assert.equal(ddb.length, 1);
  assert.deepEqual(ddb[0].Condition['ForAllValues:StringEquals']['dynamodb:LeadingKeys'], ['RECONCILIATION#PENDING', 'RECONCILIATION#RECOVERY']);
  assert.ok(ddb[0].Action.includes('dynamodb:Query'));
  assert.ok(!ddb[0].Action.includes('dynamodb:Scan'));
  assert.ok(!statements.some(s => s.Action.some(a => a.startsWith('ssm:'))));
  assert.ok(statements.some(s => s.Action.includes('sqs:SendMessage')));
  const manifest = require('../services/api-payment/service.config.json');
  assert.ok(manifest.functions.some(f => f.name === 'fn-recuperar-conciliaciones-v1' && f.entrypoint === './cmd/fn-recuperar-conciliaciones-v1'));
});
