/**
 * Barrel export for all IAM policy modules.
 *
 * Usage in serverless.ts:
 *   import { ssmReadPolicy, dynamodbCrudPolicy, s3ObjectPolicy } from '../../aws/policies';
 */
export { ssmReadPolicy, ssmWritePolicy } from './ssm';
export { dynamodbCrudPolicy, dynamodbReadPolicy, dynamodbStreamReadPolicy } from './dynamodb';
export { s3ObjectPolicy, s3ReadPolicy } from './s3';
export { sesSendPolicy } from './ses';
export { lambdaInvokePolicy } from './lambda';
export { sqsSendPolicy } from './sqs';
