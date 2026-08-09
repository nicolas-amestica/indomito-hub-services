import { STAGE } from './custom-parameters';

/**
 * Configuración del Lambda Authorizer para HTTP API Gateway v2.
 *
 * Usa payloadVersion 1.0 para compatibilidad con el formato de respuesta
 * IAM policy que retorna el authorizer.
 */
export const HTTP_API_AUTHORIZER = {
  iamAuth: {
    type: 'request' as const,
    functionArn: `\${cf:iam-auth-${STAGE}.iamAuthArn}`,
    resultTtlInSeconds: 0,
    enableSimpleResponses: false,
    payloadVersion: '1.0' as const,
    identitySource: '$request.header.Authorization',
  },
};
