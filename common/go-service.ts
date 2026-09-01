import type { AWS } from '@serverless/typescript';
import { DEPLOYMENT_BUCKET, REGION, STAGE } from './custom-parameters.js';
import { buildResourceTags } from './aws-service-tags.js';

/**
 * ID y Authorizer ID del HTTP API Gateway compartido, desplegado en
 * ind-hub-inf-aws-sls-pri-gh (modulo `api-gateway/`). Todos los microservicios
 * Go registran sus rutas en este mismo Gateway — no crean uno propio — para
 * exponer un solo dominio (api.dev.girasindomito.cl / api.girasindomito.cl)
 * sin prefijo de servicio en los paths (ver docs/standards/global/api-design.md).
 *
 * Orden de despliegue: authorizer (ind-hub-iam-tsx-sls-pri-gh) → api-gateway
 * (infra) → microservicios (este repo). Ver comentario en
 * infra/api-gateway/serverless.ts y docs/standards/architecture/auth-flow.md.
 */
const SHARED_HTTP_API_ID = `\${cf:indomito-hub-infra-api-gateway-${STAGE}.HttpApiId}`;
const SHARED_HTTP_API_AUTHORIZER_ID = `\${cf:indomito-hub-infra-api-gateway-${STAGE}.HttpApiAuthorizerId}`;

/**
 * Indica si el Gateway compartido tiene un Lambda Authorizer desplegado.
 *
 * Debe reflejar el valor de `authorizerEnabled` en
 * ind-hub-inf-aws-sls-pri-gh/api-gateway/api-gateway-config.ts.
 *
 * El authorizer vive en el repo ind-hub-iam-tsx-sls-pri-gh (stack
 * `iam-auth-<stage>`). Mientras esta constante siga en `false`, todo endpoint
 * debe declararse `public: true` — el Gateway no tiene con que autorizar un
 * endpoint protegido.
 *
 * Para activar, en este orden:
 *   1. Desplegar el authorizer: ind-hub-iam-tsx-sls-pri-gh, `make deploy stage=<stage>`
 *   2. `authorizerEnabled: true` en infra + desplegar `api-gateway`
 *   3. Poner esta constante en `true`
 */
const SHARED_AUTHORIZER_ENABLED = false;

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
 * Genera la configuración de Serverless Framework para un microservicio en Go.
 *
 * Arquitectura:
 * - AWS Lambda con runtime provided.al2023.
 * - Registra sus rutas en el HTTP API Gateway v2 COMPARTIDO (ver
 *   ind-hub-inf-aws-sls-pri-gh/api-gateway/) — no crea un Gateway propio.
 *   CORS, throttling, dominio personalizado y el Lambda Authorizer viven
 *   en ese stack compartido, no aquí (API Gateway externo no permite
 *   configurarlos por servicio — ver EXTERNAL_HTTP_API_CORS_CONFIG /
 *   EXTERNAL_HTTP_API_AUTHORIZERS_CONFIG en la documentación de Serverless).
 * - X-Ray en Lambda.
 * - Región: us-east-1 (Norte de Virginia).
 */
export function buildGoServiceServerless(service: string, endpoints: GoHttpEndpoint[], extraEnv?: Record<string, string>): AWS {
  const tags = buildResourceTags(service);

  const functions = Object.fromEntries(
    endpoints.map(endpoint => {
      const isHttpEndpoint = Boolean(endpoint.method && endpoint.path);
      const needsAuthorizer = isHttpEndpoint && !endpoint.public;

      if (needsAuthorizer && !SHARED_AUTHORIZER_ENABLED) {
        throw new Error(
          `[${service}] El endpoint "${endpoint.name}" requiere authorizer, pero el Gateway compartido no tiene uno registrado.\n` +
          `  El authorizer existe (repo ind-hub-iam-tsx-sls-pri-gh) pero aun no esta activado en el Gateway.\n` +
          `  Opciones:\n` +
          `    1. Marcar el endpoint como publico: { ..., public: true }\n` +
          `    2. Activarlo, en este orden:\n` +
          `       a. ind-hub-iam-tsx-sls-pri-gh -> make deploy stage=<stage>\n` +
          `       b. authorizerEnabled: true en\n` +
          `          ind-hub-inf-aws-sls-pri-gh/api-gateway/api-gateway-config.ts\n` +
          `          + make deploy resource=api-gateway stage=<stage>\n` +
          `       c. SHARED_AUTHORIZER_ENABLED = true en common/go-service.ts`,
        );
      }

      const httpApiEvent = isHttpEndpoint
        ? { method: endpoint.method, path: endpoint.path, ...(needsAuthorizer && { authorizer: { id: SHARED_HTTP_API_AUTHORIZER_ID } }) }
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
      // 14 dias: la ingesta y el almacenamiento de CloudWatch Logs se cobran por
      // GB. Dos semanas cubren el diagnostico de incidentes recientes, que es
      // para lo que se usan estos logs.
      logRetentionInDays: 14,
      // Sin versiones publicadas de Lambda. No usamos alias, despliegues canary
      // ni concurrencia provisionada, asi que cada version publicada solo
      // acumula una copia inmutable del paquete y consume la cuota de 75 GB de
      // almacenamiento de codigo por region. El rollback sigue disponible via
      // los artefactos del bucket de deploys.
      versionFunctions: false,
      tags,
      stackTags: tags,
      deploymentBucket: {
        name: DEPLOYMENT_BUCKET,
        // Poda los directorios de despliegues antiguos en cada deploy.
        maxPreviousDeploymentArtifacts: 3,
      },
      tracing: {
        lambda: true,
      },
      httpApi: {
        id: SHARED_HTTP_API_ID,
      },
      environment: {
        APP_NAME: service,
        APP_STAGE: '${self:provider.stage}',
        APP_REGION: '${self:provider.region}',
        APP_MODE: '${env:APP_MODE, "lambda"}',
        LOG_LEVEL: '${env:LOG_LEVEL, "info"}',
        PORT: '${env:PORT, "8080"}',
        AWS_XRAY_CONTEXT_MISSING: 'LOG_ERROR',
        ...extraEnv
      }
    },
    functions,
  };
}
