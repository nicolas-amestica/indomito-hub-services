import type { IamStatement } from './types.js';

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
