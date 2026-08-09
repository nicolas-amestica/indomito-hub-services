import type { IamStatement } from './types';

/**
 * Grants PutObject, GetObject, and DeleteObject on an S3 bucket path.
 */
export function s3ObjectPolicy(bucketArn: string): IamStatement {
  return {
    Effect: 'Allow',
    Action: ['s3:PutObject', 's3:GetObject', 's3:DeleteObject'],
    Resource: bucketArn,
  };
}

/**
 * Grants read-only access (GetObject) on an S3 bucket path.
 */
export function s3ReadPolicy(bucketArn: string): IamStatement {
  return {
    Effect: 'Allow',
    Action: ['s3:GetObject'],
    Resource: bucketArn,
  };
}
