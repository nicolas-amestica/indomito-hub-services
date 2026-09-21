import { dynamodbCrudPolicy, dynamodbReadPolicy } from '../../aws/policies/dynamodb.js';
import { ApiServices } from '../../common/api-services.js';
import { STAGE } from '../../common/custom-parameters.js';
import { buildGoServiceServerless, type GoHttpEndpoint } from '../../common/go-service.js';

/**
 * Stack de DynamoDB del repo de infraestructura
 * (ind-hub-inf-aws-sls-pri-gh/ddb/). Publica un output `<Tabla>Name` y otro
 * `<Tabla>Arn` por tabla — ver `buildTableOutputs` en ese archivo.
 */
const DDB_STACK = `indomito-hub-infra-ddb-${STAGE}`;

/**
 * ARN de la tabla `favoritos`, para las politicas IAM.
 *
 * Se referencia como string y no como objeto intrinseco (`Fn::ImportValue`)
 * porque los builders de `aws/policies/dynamodb.ts` tipan `tableArn: string` y
 * concatenan `/index/*` para cubrir los GSI. Un objeto ahi produciria
 * `"[object Object]/index/*"`.
 */
const FAVORITES_TABLE_ARN = `\${cf:${DDB_STACK}.FavoritosTableArn}`;

/**
 * Nombre de la tabla `favoritos`, que las funciones leen de
 * `FAVORITES_TABLE_NAME` (ver `functions/config.go`).
 *
 * Sale del output de CloudFormation y no de SSM porque no es un secreto sino
 * una referencia entre stacks: el nombre lo decide el stack que crea la tabla,
 * y tomarlo de ahi hace imposible que este servicio apunte a una tabla que no
 * existe.
 */
const FAVORITES_TABLE_NAME = `\${cf:${DDB_STACK}.FavoritosTableName}`;

/**
 * Endpoints del servicio. Es la unica declaracion TypeScript de su superficie
 * HTTP; la contraparte en Go son las rutas de `functions/routes.go`, y el test
 * de consistencia de ese paquete comprueba que ambas coincidan.
 *
 * Los cuatro requieren authorizer, y eso es lo que hoy impide desplegar este
 * servicio. El scope de un favorito **es** la identidad de su dueño: la clave
 * de particion de la tabla es el usuario y el identificador sale del contexto
 * del authorizer, nunca del cuerpo ni de una cabecera (Requirement 19.8).
 * Publicar estos endpoints mientras no exista authorizer permitiria a
 * cualquiera leer o borrar los favoritos de otro usuario cambiando el
 * identificador en la peticion, y como los favoritos son el unico mecanismo de
 * persistencia de la feature, eso equivale a publicar el trabajo guardado de
 * todo el equipo comercial. El allowlist de CORS del Gateway no lo evita: CORS
 * lo aplica el navegador y una peticion desde `curl` lo ignora.
 *
 * Consecuencia deliberada: mientras `SHARED_AUTHORIZER_ENABLED` siga en
 * `false` en `common/go-service.ts`, evaluar este archivo lanza un `Error` que
 * cita el Requirement 19 y explica como activar el authorizer. El servicio se
 * implementa y se prueba completo, pero no se despliega (Requirements 19.3 y
 * 19.4); hasta entonces se ejercita contra el servidor local
 * (`make dev service=services/api-favorite`, Requirement 19.5).
 *
 * Memoria y timeout: 128 MB y 6 s para los cuatro. Son lecturas y escrituras
 * simples de un item por clave completa, o un `Query` sobre una sola particion;
 * no procesan volumen ni llaman a servicios externos.
 */
const endpoints: GoHttpEndpoint[] = [
  {
    name: 'fn-listar-favoritos-v1',
    method: 'GET',
    path: '/favoritos',
    public: false,
    memorySize: 128,
    timeout: 6,
    description: 'Lista los favoritos del usuario autenticado, por scope',
    // Solo lectura: es el unico de los cuatro que no modifica la tabla.
    policies: [dynamodbReadPolicy(FAVORITES_TABLE_ARN)],
  },
  {
    name: 'fn-crear-favorito-v1',
    method: 'POST',
    path: '/favoritos',
    public: false,
    memorySize: 128,
    timeout: 6,
    description: 'Crea un favorito con el contenido del programa en curso',
    policies: [dynamodbCrudPolicy(FAVORITES_TABLE_ARN)],
  },
  {
    name: 'fn-actualizar-favorito-v1',
    method: 'PUT',
    path: '/favoritos/{id-favorito}',
    public: false,
    memorySize: 128,
    timeout: 6,
    description: 'Reemplaza el contenido de un favorito del usuario autenticado',
    policies: [dynamodbCrudPolicy(FAVORITES_TABLE_ARN)],
  },
  {
    name: 'fn-eliminar-favorito-v1',
    method: 'DELETE',
    path: '/favoritos/{id-favorito}',
    public: false,
    memorySize: 128,
    timeout: 6,
    description: 'Elimina un favorito del usuario autenticado',
    policies: [dynamodbCrudPolicy(FAVORITES_TABLE_ARN)],
  },
];

const config = buildGoServiceServerless(ApiServices.Favorite, endpoints, {
  env: {
    FAVORITES_TABLE_NAME,
  },
});

module.exports = config;
