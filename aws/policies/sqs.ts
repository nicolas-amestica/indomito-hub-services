import type { IamResource, IamStatement } from './types';

/**
 * Grants sqs:SendMessage on the specified queue ARN(s).
 */
export function sqsSendPolicy(queueArn: IamResource): IamStatement {
  return {
    Effect: 'Allow',
    Action: ['sqs:SendMessage'],
    Resource: queueArn,
  };
}
