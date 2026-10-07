import type { AWS } from '@serverless/typescript';
import { DEPLOYMENT_BUCKET, REGION, STAGE } from '../../common/custom-parameters.js';

if (STAGE !== 'dev') throw new Error('Comprobantes habilitados exclusivamente para DEV');

const tableArn = '${cf:indomito-hub-infra-ddb-dev.PagosTableArn}';
const streamArn = '${cf:indomito-hub-infra-ddb-dev.PagosTableStreamArn}';
const bucketArn = '${cf:indomito-hub-infra-s3-dev.ReceiptsBucketArn}';

const configuration: AWS = {
  service: 'payment-receipts', frameworkVersion: '4',
  provider: {
    name: 'aws', runtime: 'provided.al2023', architecture: 'arm64', region: REGION, stage: STAGE,
    deploymentBucket: { name: DEPLOYMENT_BUCKET }, memorySize: 128, timeout: 60, logRetentionInDays: 14,
    environment: { APP_STAGE: 'dev', PAYMENTS_TABLE_NAME: '${cf:indomito-hub-infra-ddb-dev.PagosTableName}', RECEIPTS_BUCKET_NAME: '${cf:indomito-hub-infra-s3-dev.ReceiptsBucketName}', RECEIPT_VERIFICATION_URL: 'https://pagos.dev.girasindomito.cl/verificar-comprobante' },
    iam: { role: { statements: [
      { Effect: 'Allow', Action: ['dynamodb:GetItem', 'dynamodb:UpdateItem'], Resource: tableArn, Condition: { 'ForAllValues:StringLike': { 'dynamodb:LeadingKeys': ['RECEIPT#*', 'JOB#*'] } } },
      { Effect: 'Allow', Action: ['dynamodb:DescribeStream', 'dynamodb:GetRecords', 'dynamodb:GetShardIterator'], Resource: streamArn },
      { Effect: 'Allow', Action: ['dynamodb:ListStreams'], Resource: tableArn },
      { Effect: 'Allow', Action: ['s3:PutObject', 's3:GetObject'], Resource: `${bucketArn}/receipts/*` },
      { Effect: 'Allow', Action: ['ssm:GetParameter'], Resource: { 'Fn::Sub': 'arn:${AWS::Partition}:ssm:${AWS::Region}:${AWS::AccountId}:parameter/indomito/dev/payments/receipt-smtp' } },
      { Effect: 'Allow', Action: ['sqs:SendMessage'], Resource: { 'Fn::GetAtt': ['ReceiptFailures', 'Arn'] } },
    ] } },
  },
  package: { individually: true },
  functions: { 'fn-procesar-comprobantes-v1': { handler: 'bootstrap', package: { artifact: '.serverless-artifacts/fn-procesar-comprobantes-v1.zip' } } },
  resources: { Resources: {
    ReceiptFailures: { Type: 'AWS::SQS::Queue', Properties: { MessageRetentionPeriod: 1209600, SqsManagedSseEnabled: true } },
    ReceiptStream: { Type: 'AWS::Lambda::EventSourceMapping', Properties: {
      EventSourceArn: streamArn, FunctionName: { 'Fn::GetAtt': ['FnDashprocesarDashcomprobantesDashv1LambdaFunction', 'Arn'] }, StartingPosition: 'TRIM_HORIZON', BatchSize: 1,
      MaximumRetryAttempts: 5, MaximumRecordAgeInSeconds: 3600, BisectBatchOnFunctionError: true, FunctionResponseTypes: ['ReportBatchItemFailures'],
      DestinationConfig: { OnFailure: { Destination: { 'Fn::GetAtt': ['ReceiptFailures', 'Arn'] } } },
      FilterCriteria: { Filters: [{ Pattern: JSON.stringify({ eventName: ['INSERT', 'MODIFY'], dynamodb: { NewImage: { pk: { S: [{ prefix: 'JOB#' }] }, status: { S: ['PENDING'] } } } }) }] },
    } },
  }, Outputs: { ReceiptFailuresUrl: { Value: { Ref: 'ReceiptFailures' } } } },
};

module.exports = configuration;
