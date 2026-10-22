import type { AWS } from '@serverless/typescript';
import { DEPLOYMENT_BUCKET, REGION, STAGE } from '../../common/custom-parameters.js';

const tableArn = `\${cf:indomito-hub-infra-ddb-${STAGE}.PagosTableArn}`;
const tableName = `\${cf:indomito-hub-infra-ddb-${STAGE}.PagosTableName}`;
const topic = { Ref: 'RenditionAlerts' };
const namespace = 'Indomito/Renditions';
const dimensions = [{ Name: 'Stage', Value: STAGE }];
const resources: Record<string, unknown> = {
  RenditionAlerts: { Type: 'AWS::SNS::Topic' },
  AlertDeliveryFailures: { Type: 'AWS::SQS::Queue', Properties: { MessageRetentionPeriod: 1209600, SqsManagedSseEnabled: true } },
  AlertTopicPolicy: { Type: 'AWS::SNS::TopicPolicy', Properties: { Topics: [topic], PolicyDocument: { Version: '2012-10-17', Statement: [{ Effect: 'Allow', Principal: { Service: 'cloudwatch.amazonaws.com' }, Action: 'sns:Publish', Resource: topic,
    Condition: { StringEquals: { 'aws:SourceAccount': { Ref: 'AWS::AccountId' } }, ArnLike: { 'aws:SourceArn': { 'Fn::Sub': `arn:\${AWS::Partition}:cloudwatch:\${AWS::Region}:\${AWS::AccountId}:alarm:khipu-renditions-${STAGE}-*` } } } }] } } },
};
function alarm(id: string, metric: string, ns: string, dims: unknown[], period = 300, threshold = 1, missing = 'notBreaching', comparison = 'GreaterThanOrEqualToThreshold', notify = true) {
  resources[id] = { Type: 'AWS::CloudWatch::Alarm', Properties: {
    AlarmName: `khipu-renditions-${STAGE}-${id}`, AlarmDescription: 'Revisar Tesorería > Rendiciones y logs. No conciliar manualmente sin revisar evidencia.',
    Namespace: ns, MetricName: metric, Dimensions: dims, Statistic: 'Sum', Period: period,
    EvaluationPeriods: 1, Threshold: threshold, ComparisonOperator: comparison, TreatMissingData: missing,
    ...(notify ? { AlarmActions: [topic], OKActions: [topic] } : {}),
  } };
}
for (const [id, name] of [['Receiver', 'fn-recibir-rendicion-khipu-v1'], ['Worker', 'fn-procesar-rendiciones-khipu-v1']]) {
  const dims = [{ Name: 'FunctionName', Value: `api-payment-${STAGE}-${name}` }];
  alarm(`${id}Errors`, 'Errors', 'AWS/Lambda', dims);
  alarm(`${id}Throttles`, 'Throttles', 'AWS/Lambda', dims);
  if (id === 'Worker') alarm('WorkerNotRunning', 'Invocations', 'AWS/Lambda', dims, 900, 1, 'breaching', 'LessThanThreshold');
}
alarm('ReceiverRejected', 'ReceiverRejected', namespace, dimensions);
alarm('MonitorNotHealthy', 'MonitorHeartbeat', namespace, dimensions, 3600, 1, 'breaching', 'LessThanThreshold');
alarm('MonitorErrors', 'Errors', 'AWS/Lambda', [{ Name: 'FunctionName', Value: `payment-monitoring-${STAGE}-monitor` }]);
// Sin acción circular: si SMTP falla, queda alarma y cola de fallos en AWS.
alarm('EmailDeliveryErrors', 'Errors', 'AWS/Lambda', [{ Name: 'FunctionName', Value: `payment-monitoring-${STAGE}-alarm` }], 300, 1, 'notBreaching', 'GreaterThanOrEqualToThreshold', false);
alarm('EmailDeadLetters', 'ApproximateNumberOfMessagesVisible', 'AWS/SQS', [{ Name: 'QueueName', Value: { 'Fn::GetAtt': ['AlertDeliveryFailures', 'QueueName'] } }], 300, 1, 'notBreaching', 'GreaterThanOrEqualToThreshold', false);

const configuration: AWS = {
  service: 'payment-monitoring', frameworkVersion: '4',
  provider: {
    name: 'aws', runtime: 'provided.al2023', architecture: 'arm64', stage: STAGE, region: REGION,
    deploymentBucket: { name: DEPLOYMENT_BUCKET }, timeout: 180, memorySize: 256, logRetentionInDays: 30,
    environment: { APP_STAGE: STAGE, PAYMENTS_TABLE_NAME: tableName, ALARM_TOPIC_ARN: topic },
    iam: { role: { statements: [
      { Effect: 'Allow', Action: ['ssm:GetParameter'], Resource: ['receipt-smtp', 'khipu/renditions/monitoring-config'].map(suffix => ({ 'Fn::Sub': `arn:\${AWS::Partition}:ssm:\${AWS::Region}:\${AWS::AccountId}:parameter/indomito/${STAGE}/payments/${suffix}` })) },
      { Effect: 'Allow', Action: ['dynamodb:Query', 'dynamodb:GetItem'], Resource: tableArn, Condition: { 'ForAllValues:StringLike': { 'dynamodb:LeadingKeys': ['RENDITION_MONTH#*', 'RENDITION#*', 'CASH#*', 'TRIP#*', 'RENDITION_MONITOR'] } } },
      { Effect: 'Allow', Action: ['dynamodb:PutItem', 'dynamodb:DeleteItem'], Resource: tableArn, Condition: { 'ForAllValues:StringEquals': { 'dynamodb:LeadingKeys': ['RENDITION_MONITOR'] } } },
      { Effect: 'Allow', Action: ['sqs:SendMessage'], Resource: { 'Fn::GetAtt': ['AlertDeliveryFailures', 'Arn'] } },
    ] } },
  },
  package: { individually: true },
  functions: {
    monitor: { handler: 'bootstrap', package: { artifact: '.serverless-artifacts/monitor.zip' }, events: [{ schedule: 'rate(15 minutes)' }], maximumRetryAttempts: 2 },
    alarm: { handler: 'bootstrap', package: { artifact: '.serverless-artifacts/alarm.zip' }, timeout: 60, events: [{ sns: { arn: topic, topicName: 'RenditionAlerts' } }],
      onError: { 'Fn::GetAtt': ['AlertDeliveryFailures', 'Arn'] }, maximumRetryAttempts: 2 },
  },
  resources: { Resources: resources as NonNullable<NonNullable<AWS['resources']>['Resources']> },
};
module.exports = configuration;
