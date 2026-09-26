import { dynamodbCrudPolicy, dynamodbReadPolicy } from '../../aws/policies/dynamodb.js';
import { ApiServices } from '../../common/api-services.js';
import { STAGE } from '../../common/custom-parameters.js';
import { buildGoServiceServerless, type GoHttpEndpoint } from '../../common/go-service.js';

const DDB_STACK = `indomito-hub-infra-ddb-${STAGE}`;
const PROGRAMS_TABLE_ARN = `\${cf:${DDB_STACK}.ProgramasTableArn}`;
const PROGRAMS_TABLE_NAME = `\${cf:${DDB_STACK}.ProgramasTableName}`;

/**
 * Endpoints del servicio. Es la unica declaracion TypeScript de su superficie
 * HTTP; la contraparte en Go es `GenerateBudgetRoute` de `functions/routes.go`,
 * y el test de consistencia de ese paquete comprueba que ambas coincidan.
 *
 * Uno solo, y eso es el Requirement 17.1: `api-program` expone unicamente el
 * endpoint del presupuesto.
 *
 * **Sin `policies`.** Ninguna, y la ausencia es el punto. El builder de
 * `common/go-service.ts` solo arma un `AWS::IAM::Role` propio para una funcion
 * que declare `policies`, y solo emite `provider.iam` si el servicio declara
 * `options.policies`. Como aqui no hay ni lo uno ni lo otro, este servicio se
 * despliega con el rol compartido que Serverless genera por defecto: logs de su
 * propio log group y X-Ray, nada mas. No alcanza ninguna tabla de DynamoDB.
 *
 * Eso implementa los Requirements 17.9 y 17.12 por construccion, no por
 * disciplina: el servicio recibe el programa y los montos por escenario ya
 * calculados en el cuerpo de la peticion y solo los maqueta, asi que no
 * necesita leer nada. `functions/config.go` tampoco declara nombre de tabla.
 * Agregar aqui una politica de DynamoDB no seria un ajuste de permisos sino un
 * cambio de contrato del servicio.
 *
 * Requiere authorizer, y eso es lo que hoy impide desplegarlo. El endpoint
 * genera un documento comercial con los precios de una cotizacion e invoca
 * compute facturable de 512 MB por llamada; publicarlo sin authorizer lo dejaria
 * accesible a cualquiera que conozca la URL, tanto para producir presupuestos
 * con el diseno de la empresa como para consumir su cuota de Lambda. El
 * allowlist de CORS del Gateway no lo evita: CORS lo aplica el navegador y una
 * peticion desde `curl` lo ignora.
 *
 * Consecuencia deliberada: mientras `SHARED_AUTHORIZER_ENABLED` siga en `false`
 * en `common/go-service.ts`, evaluar este archivo lanza un `Error` que cita el
 * Requirement 19 y explica como activar el authorizer. El servicio se implementa
 * y se prueba completo, pero no se despliega (Requirements 19.3 y 19.4); hasta
 * entonces se ejercita contra el servidor local
 * (`make dev service=services/api-program`, Requirement 19.5).
 *
 * Memoria y timeout: 512 MB y 20 s. No es por aritmetica — los precios llegan
 * calculados — sino porque el PDF se compone entero en memoria y se devuelve en
 * base64 sin pasar por S3: la huella la fija el documento, no el calculo. Son
 * los mismos 512 MB que hacen que dejar este endpoint publico sea tambien un
 * problema de costo.
 */
const endpoints: GoHttpEndpoint[] = [
  { name: 'fn-listar-cotizaciones-v1', method: 'GET', path: '/cotizaciones', public: false, memorySize: 128, timeout: 6, description: 'Lista las cotizaciones del usuario autenticado', policies: [dynamodbReadPolicy(PROGRAMS_TABLE_ARN)] },
  { name: 'fn-crear-cotizacion-v1', method: 'POST', path: '/cotizaciones', public: false, memorySize: 128, timeout: 6, description: 'Crea una cotizacion para el usuario autenticado', policies: [dynamodbCrudPolicy(PROGRAMS_TABLE_ARN)] },
  { name: 'fn-actualizar-cotizacion-v1', method: 'PUT', path: '/cotizaciones/{id-cotizacion}', public: false, memorySize: 128, timeout: 6, description: 'Actualiza una cotizacion del usuario autenticado', policies: [dynamodbCrudPolicy(PROGRAMS_TABLE_ARN)] },
  { name: 'fn-eliminar-cotizacion-v1', method: 'DELETE', path: '/cotizaciones/{id-cotizacion}', public: false, memorySize: 128, timeout: 6, description: 'Elimina una cotizacion del usuario autenticado', policies: [dynamodbCrudPolicy(PROGRAMS_TABLE_ARN)] },
  {
    name: 'fn-generar-presupuesto-v1',
    method: 'POST',
    path: '/cotizaciones:presupuesto',
    public: false,
    memorySize: 512,
    timeout: 20,
    description: 'Genera el PDF de presupuesto del programa con los precios por escenario recibidos',
  },
];

const config = buildGoServiceServerless(ApiServices.Program, endpoints, {
  env: { PROGRAMS_TABLE_NAME },
});

module.exports = config;
