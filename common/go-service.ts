import type { AWS } from '@serverless/typescript';
import type { IamStatement } from '../aws/policies/types.js';
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
const SHARED_AUTHORIZER_ENABLED = STAGE === 'dev';

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
  /**
   * Sentencias IAM exclusivas de esta función, construidas con los builders de
   * `aws/policies/`.
   *
   * Declarar el permiso por función y no por servicio es lo que mantiene el
   * principio de mínimo privilegio dentro de un mismo stack: un endpoint de
   * lectura no recibe escritura solo porque su vecino la necesite. Una función
   * que no declara nada se queda con el rol compartido del servicio, que no
   * alcanza ninguna tabla.
   *
   * @example
   * { name: 'fn-obtener-catalogos-v1', policies: [dynamodbReadPolicy(CATALOGS_TABLE_ARN)] }
   */
  policies?: IamStatement[];
};

/**
 * Opciones a nivel de servicio para {@link buildGoServiceServerless}.
 */
export type GoServiceOptions = {
  /** Variables de entorno adicionales para todas las funciones del servicio. */
  env?: Record<string, string>;
  /**
   * Sentencias IAM que aplican a todas las funciones del servicio (por ejemplo,
   * la lectura de un parámetro SSM que todas necesitan).
   *
   * Se emiten en `provider.iam` y además se copian en el rol propio de cada
   * función que declare `policies`: una función con rol propio no hereda el rol
   * compartido, así que sin esa copia perdería estos permisos.
   */
  policies?: IamStatement[];
};

/** Recurso de CloudFormation tal como lo tipa @serverless/typescript. */
type CfResource = NonNullable<NonNullable<AWS['resources']>['Resources']>[string];

/**
 * Convierte el nombre de una función (`fn-obtener-catalogos-v1`) en un ID lógico
 * de CloudFormation (`FnObtenerCatalogosV1`). Los IDs lógicos solo admiten
 * caracteres alfanuméricos.
 */
function toLogicalId(functionName: string): string {
  return functionName
    .split(/[^a-zA-Z0-9]+/)
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join('');
}

/**
 * Sentencias que Serverless agrega por defecto al rol compartido del servicio y
 * que hay que reponer al armar un rol propio por función.
 *
 * - Logs: acotados al log group de esa única función, no a los del stack completo.
 * - X-Ray: `Resource: '*'` es lo único que acepta la API de X-Ray, que no admite
 *   permisos a nivel de recurso. Se incluye porque `provider.tracing.lambda`
 *   está activo.
 */
function lambdaBaseStatements(service: string, functionName: string): IamStatement[] {
  // El nombre del log group replica el que arma Serverless: <servicio>-<stage>-<funcion>.
  // Se usa la constante STAGE y no `${self:provider.stage}` porque dentro de un
  // Fn::Sub conviene dejar solo pseudo-parametros de CloudFormation: una variable
  // de Serverless sin resolver ahi se convierte en un error de despliegue.
  const logGroupArn =
    `arn:\${AWS::Partition}:logs:\${AWS::Region}:\${AWS::AccountId}:log-group:` +
    `/aws/lambda/${service}-${STAGE}-${functionName}`;

  return [
    {
      Effect: 'Allow',
      Action: ['logs:CreateLogStream', 'logs:PutLogEvents'],
      Resource: { 'Fn::Sub': `${logGroupArn}:*` },
    },
    {
      Effect: 'Allow',
      Action: ['xray:PutTraceSegments', 'xray:PutTelemetryRecords'],
      Resource: '*',
    },
  ];
}

/**
 * Arma el `AWS::IAM::Role` propio de una función.
 *
 * Sin `RoleName`: CloudFormation genera uno único y así se evita el límite de
 * 64 caracteres, que un nombre compuesto por servicio + stage + función supera
 * con facilidad. Las tags las propaga `provider.stackTags`.
 */
function buildFunctionRole(functionName: string, statements: IamStatement[]): CfResource {
  return {
    Type: 'AWS::IAM::Role',
    Properties: {
      AssumeRolePolicyDocument: {
        Version: '2012-10-17',
        Statement: [
          {
            Effect: 'Allow',
            Principal: { Service: ['lambda.amazonaws.com'] },
            Action: ['sts:AssumeRole'],
          },
        ],
      },
      Policies: [
        {
          PolicyName: `${functionName}-policy`,
          PolicyDocument: {
            Version: '2012-10-17',
            Statement: statements,
          },
        },
      ],
    },
  } as CfResource;
}

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
 *
 * Permisos IAM:
 * - `options.policies` va al rol compartido del servicio (`provider.iam`).
 * - `endpoint.policies` genera un `AWS::IAM::Role` propio para esa función, con
 *   sus sentencias más las compartidas más las de logs y X-Ray. Un rol propio no
 *   hereda el compartido, así que las compartidas se copian.
 * - Una función sin `policies` usa el rol compartido del servicio. Si el servicio
 *   tampoco declara nada, ese rol no alcanza ninguna tabla de DynamoDB.
 *
 * @example
 * buildGoServiceServerless(ApiServices.Catalog, [
 *   { name: 'fn-obtener-catalogos-v1', method: 'GET', path: '/catalogos', public: true,
 *     policies: [dynamodbReadPolicy(CATALOGS_TABLE_ARN)] },
 *   { name: 'fn-obtener-tasas-cambio-v1', method: 'GET', path: '/tasas-cambio', public: true,
 *     policies: [dynamodbCrudPolicy(CATALOGS_TABLE_ARN)] },
 * ]);
 */
export function buildGoServiceServerless(service: string, endpoints: GoHttpEndpoint[], options: GoServiceOptions = {}): AWS {
  const tags = buildResourceTags(service);
  const sharedStatements = options.policies ?? [];
  const functionRoles: Record<string, CfResource> = {};

  const functions = Object.fromEntries(
    endpoints.map(endpoint => {
      const isHttpEndpoint = Boolean(endpoint.method && endpoint.path);
      const needsAuthorizer = isHttpEndpoint && !endpoint.public;

      if (needsAuthorizer && !SHARED_AUTHORIZER_ENABLED) {
        throw new Error(
          `[${service}] El endpoint "${endpoint.name}" requiere authorizer, pero el Gateway compartido no tiene uno registrado.\n` +
          `  El authorizer existe (repo ind-hub-iam-tsx-sls-pri-gh) pero aun no esta activado en el Gateway.\n` +
          `  Este corte es deliberado: el Requirement 19 (19.3 y 19.4) impide desplegar un\n` +
          `  endpoint que dependa de la identidad del usuario mientras no exista con que\n` +
          `  autorizarlo. Sin authorizer, publicar el CRUD de favoritos permitiria a\n` +
          `  cualquiera leer o borrar los favoritos de otro usuario cambiando el\n` +
          `  identificador en la peticion. No lo resuelvas marcando el endpoint como\n` +
          `  publico si su comportamiento depende del usuario que llama.\n` +
          `  Opciones:\n` +
          `    1. Si el endpoint no depende de la identidad de quien llama,\n` +
          `       marcarlo publico: { ..., public: true }\n` +
          `    2. Activarlo, en este orden:\n` +
          `       a. ind-hub-iam-tsx-sls-pri-gh -> make deploy stage=<stage>\n` +
          `       b. authorizerEnabled: true en\n` +
          `          ind-hub-inf-aws-sls-pri-gh/api-gateway/api-gateway-config.ts\n` +
          `          + make deploy resource=api-gateway stage=<stage>\n` +
          `       c. SHARED_AUTHORIZER_ENABLED = true en common/go-service.ts`,
        );
      }

      const httpApiEvent = isHttpEndpoint
        ? {
            method: endpoint.method,
            path: endpoint.path,
            ...(needsAuthorizer && {
              authorizer: { type: 'request', id: SHARED_HTTP_API_AUTHORIZER_ID },
            }),
          }
        : null;
      const defaultEvents: GoFunctionEvent[] = httpApiEvent ? [{ httpApi: httpApiEvent }] : [];

      // Solo las funciones que declaran permisos reciben rol propio. El resto se
      // queda con el rol compartido del servicio, que no llega a DynamoDB.
      const ownStatements = endpoint.policies ?? [];
      const needsOwnRole = ownStatements.length > 0;
      let roleLogicalId: string | undefined;

      if (needsOwnRole) {
        roleLogicalId = `${toLogicalId(endpoint.name)}IamRole`;
        functionRoles[roleLogicalId] = buildFunctionRole(endpoint.name, [
          ...lambdaBaseStatements(service, endpoint.name),
          ...sharedStatements,
          ...ownStatements,
        ]);
      }

      return [
        endpoint.name,
        {
          handler: 'bootstrap',
          ...(roleLogicalId && { role: roleLogicalId }),
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
      // Rol compartido del servicio: lo usan las funciones que no declaran
      // `policies` propias. Sin sentencias compartidas queda con lo que
      // Serverless agrega por defecto (logs y X-Ray) y nada más, de modo que una
      // funcion sin permisos declarados no puede alcanzar ninguna tabla.
      ...(sharedStatements.length > 0 && {
        // El cast es necesario porque @serverless/typescript tipa el ARN de una
        // sentencia como string y aquí puede ser un objeto Fn::Sub / Fn::ImportValue,
        // que es como los servicios importan el ARN de su tabla desde infra.
        iam: { role: { statements: sharedStatements } } as AWS['provider']['iam'],
      }),
      environment: {
        APP_NAME: service,
        APP_STAGE: '${self:provider.stage}',
        APP_REGION: '${self:provider.region}',
        APP_MODE: '${env:APP_MODE, "lambda"}',
        LOG_LEVEL: '${env:LOG_LEVEL, "info"}',
        PORT: '${env:PORT, "8080"}',
        AWS_XRAY_CONTEXT_MISSING: 'LOG_ERROR',
        ...options.env
      }
    },
    functions,
    // Un rol por función que declare permisos. Se omite la clave completa si
    // ninguna los declara, para no emitir un bloque `resources` vacío.
    ...(Object.keys(functionRoles).length > 0 && {
      resources: {
        Resources: functionRoles,
      },
    }),
  };
}
