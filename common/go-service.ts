import type { AWS } from '@serverless/typescript';
import { CORS_ORIGINS, RATE_LIMIT, REGION, REGION_CODE, STAGE } from './custom-parameters';
import { buildResourceTags } from './aws-service-tags';
import { HTTP_API_AUTHORIZER } from './lambda-authorizer';

/**
 * Evento personalizado para una función Lambda (EventBridge, DynamoDB Stream, etc.).
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type GoFunctionEvent = Record<string, any>;

/**
 * Define la estructura de una función Lambda para servicios en Go.
 * - Para endpoints HTTP: definir `method` y `path` (genera httpApi event automáticamente).
 * - Para otros triggers: definir `events` con los eventos deseados.
 */
export type GoHttpEndpoint = {
  name: string;
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  path?: string;
  timeout?: number;
  memorySize?: number;
  description?: string;
  /** Marca el endpoint como público (sin authorizer). */
  public?: boolean;
  /** Eventos adicionales o alternativos. */
  events?: GoFunctionEvent[];
  /** Variables de entorno adicionales exclusivas para esta función. */
  env?: Record<string, string>;
};

/**
 * Configuración global de CORS para HTTP API Gateway v2.
 */
const httpApiCorsConfig = {
  allowedOrigins: CORS_ORIGINS,
  allowedHeaders: ['Content-Type', 'Authorization', 'X-Request-Id', 'X-Correlation-Id'],
  allowedMethods: ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'],
  allowCredentials: true,
  maxAge: 86400,
};

/**
 * Formato JSON para access logs de API Gateway HTTP API.
 */
const httpApiAccessLogFormat = JSON.stringify({
  requestId: '$context.requestId',
  routeKey: '$context.routeKey',
  status: '$context.status',
  httpMethod: '$context.httpMethod',
  path: '$context.path',
  protocol: '$context.protocol',
  responseLength: '$context.responseLength',
  responseLatency: '$context.responseLatency',
  integrationStatus: '$context.integrationStatus',
  integrationLatency: '$context.integrationLatency',
  integrationErrorMessage: '$context.integrationErrorMessage',
  ip: '$context.identity.sourceIp',
  userAgent: '$context.identity.userAgent',
  requestTime: '$context.requestTime',
});

/**
 * Genera la configuración de Serverless Framework para un microservicio en Go.
 *
 * Arquitectura:
 * - AWS Lambda con runtime provided.al2023.
 * - API Gateway HTTP API v2.
 * - URL por defecto de AWS (sin custom domain por ahora).
 * - CORS restrictivo.
 * - X-Ray en Lambda.
 * - Métricas detalladas en API Gateway.
 * - Throttling global del stage.
 * - Access logs de API Gateway.
 * - Región: us-east-1 (Norte de Virginia).
 */
export function buildGoServiceServerless(service: string, endpoints: GoHttpEndpoint[], extraEnv?: Record<string, string>): AWS {
  const tags = buildResourceTags(service);
  const deploymentBucket = `ind-hub-${STAGE}-deploys-s3-${STAGE}-pri-${REGION_CODE}`;

  const functions = Object.fromEntries(
    endpoints.map(endpoint => {
      const httpApiEvent = endpoint.method && endpoint.path
        ? { method: endpoint.method, path: endpoint.path, ...(!endpoint.public && { authorizer: { name: 'iamAuth' } }) }
        : null;
      const defaultEvents: GoFunctionEvent[] = httpApiEvent ? [{ httpApi: httpApiEvent }] : [];

      return [
        endpoint.name,
        {
          handler: 'bootstrap',
          description: endpoint.description ?? (endpoint.method && endpoint.path ? `${endpoint.method} ${endpoint.path}` : endpoint.name),
          timeout: endpoint.timeout,
          memorySize: endpoint.memorySize,
          environment: {
            APP_FUNCTION_NAME: endpoint.name,
            ...endpoint.env,
          },
          package: {
            artifact: `.serverless-artifacts/${endpoint.name}.zip`
          },
          events: endpoint.events ?? defaultEvents
        }
      ];
    })
  ) as unknown as AWS['functions'];

  return {
    service,
    frameworkVersion: '4',
    useDotenv: true,

    package: {
      individually: true,
      patterns: ['!./**']
    },

    plugins: ['serverless-offline'],

    provider: {
      name: 'aws',
      runtime: 'provided.al2023',
      architecture: 'arm64',
      region: REGION,
      stage: STAGE,
      timeout: 6,
      memorySize: 128,
      logRetentionInDays: 90,
      tags,
      stackTags: tags,
      deploymentBucket: { name: deploymentBucket },
      tracing: {
        lambda: true,
      },
      httpApi: {
        metrics: true,
        cors: httpApiCorsConfig,
        useProviderTags: true,
        authorizers: HTTP_API_AUTHORIZER,
      },
      environment: {
        APP_NAME: service,
        APP_STAGE: '${self:provider.stage}',
        APP_REGION: '${self:provider.region}',
        APP_MODE: '${env:APP_MODE, "lambda"}',
        LOG_LEVEL: '${env:LOG_LEVEL, "info"}',
        PORT: '${env:PORT, "8080"}',
        AWS_XRAY_CONTEXT_MISSING: 'LOG_ERROR',
        CORS_ORIGINS: CORS_ORIGINS.find(origin => !origin.includes('localhost')) ?? CORS_ORIGINS[0] ?? '',
        ...extraEnv
      }
    },
    functions,

    resources: {
      extensions: {
        HttpApiStage: {
          Properties: {
            DefaultRouteSettings: {
              DetailedMetricsEnabled: true,
              ThrottlingBurstLimit: RATE_LIMIT.burstLimit,
              ThrottlingRateLimit: RATE_LIMIT.maxRequestsPerSecond,
            },
            AccessLogSettings: {
              DestinationArn: {
                'Fn::GetAtt': ['HttpApiAccessLogGroup', 'Arn'],
              },
              Format: httpApiAccessLogFormat,
            },
          },
        },
      },
      Resources: {
        HttpApiAccessLogGroup: {
          Type: 'AWS::Logs::LogGroup',
          Properties: {
            LogGroupName: `/aws/apigateway/${service}-${STAGE}`,
            RetentionInDays: 90,
            Tags: tags,
          },
        },
      },
    } as AWS['resources'],
  };
}
