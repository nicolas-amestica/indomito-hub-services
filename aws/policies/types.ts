/**
 * Resource ARN(s) for an IAM statement. Accepts a plain string, a list of
 * strings, or a CloudFormation intrinsic object for ARNs resolved at deploy time.
 */
export type IamResource = string | string[] | Record<string, unknown>;

/**
 * IAM policy statement compatible with Serverless Framework.
 */
export interface IamStatement {
  [key: string]: unknown;
  Effect: 'Allow' | 'Deny';
  Action: string[];
  Resource: IamResource;
}
