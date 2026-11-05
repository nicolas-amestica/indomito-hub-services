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
