import type { IamStatement } from './types.js';

/**
 * Grants ssm:GetParameter on the specified parameter path pattern.
 */
export function ssmReadPolicy(parameterPattern: string): IamStatement {
  return {
    Effect: 'Allow',
    Action: ['ssm:GetParameter', 'ssm:GetParameters'],
    Resource: parameterPattern,
  };
}

/**
 * Grants ssm:PutParameter on the specified parameter path pattern.
 */
export function ssmWritePolicy(parameterPattern: string): IamStatement {
  return {
    Effect: 'Allow',
    Action: ['ssm:PutParameter'],
    Resource: parameterPattern,
  };
}
