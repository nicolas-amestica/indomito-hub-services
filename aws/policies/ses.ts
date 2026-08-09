import type { IamStatement } from './types';

/**
 * Grants ses:SendEmail and ses:SendRawEmail on the specified identity ARN(s).
 */
export function sesSendPolicy(identityArn: string | string[]): IamStatement {
  return {
    Effect: 'Allow',
    Action: ['ses:SendEmail', 'ses:SendRawEmail'],
    Resource: identityArn,
  };
}
