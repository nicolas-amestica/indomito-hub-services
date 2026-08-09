import type { IamStatement } from './types';

/**
 * Generates an IAM statement to allow invoking a Lambda function.
 */
export function lambdaInvokePolicy(functionArn: string | string[]): IamStatement {
  return {
    Effect: 'Allow',
    Action: ['lambda:InvokeFunction'],
    Resource: Array.isArray(functionArn) ? functionArn : [functionArn],
  };
}
