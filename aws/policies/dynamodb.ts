import type { IamResource, IamStatement } from './types.js';

/**
 * Grants full CRUD operations on a DynamoDB table and its GSI indexes.
 */
export function dynamodbCrudPolicy(tableArn: string): IamStatement {
  return {
    Effect: 'Allow',
    Action: [
      'dynamodb:Query',
      'dynamodb:GetItem',
      'dynamodb:PutItem',
      'dynamodb:UpdateItem',
      'dynamodb:DeleteItem',
      'dynamodb:BatchGetItem',
      'dynamodb:BatchWriteItem',
    ],
    Resource: [tableArn, `${tableArn}/index/*`],
  };
}

/**
 * Grants read-only operations on a DynamoDB table and its GSI indexes.
 */
export function dynamodbReadPolicy(tableArn: string): IamStatement {
  return {
    Effect: 'Allow',
    Action: ['dynamodb:Query', 'dynamodb:GetItem', 'dynamodb:BatchGetItem'],
    Resource: [tableArn, `${tableArn}/index/*`],
  };
}

/**
 * Grants read access to a DynamoDB Stream.
 */
export function dynamodbStreamReadPolicy(streamArn: IamResource): IamStatement {
  return {
    Effect: 'Allow',
    Action: [
      'dynamodb:DescribeStream',
      'dynamodb:GetRecords',
      'dynamodb:GetShardIterator',
      'dynamodb:ListStreams',
    ],
    Resource: streamArn,
  };
}
