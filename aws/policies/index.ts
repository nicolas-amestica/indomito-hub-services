/**
 * Barrel export for all IAM policy modules.
 *
 * Usage in serverless.ts:
 *   import { ssmReadPolicy, dynamodbCrudPolicy, s3ObjectPolicy } from '../../aws/policies';
 */
export { ssmReadPolicy, ssmWritePolicy } from './ssm.js';
export { dynamodbCrudPolicy, dynamodbReadPolicy, dynamodbStreamReadPolicy } from './dynamodb.js';
export { s3ObjectPolicy, s3ReadPolicy } from './s3.js';
export { lambdaInvokePolicy } from './lambda.js';
