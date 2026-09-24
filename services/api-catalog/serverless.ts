import {
  dynamodbCrudPolicy,
  dynamodbReadPolicy,
} from "../../aws/policies/dynamodb.js";
import { ApiServices } from "../../common/api-services.js";
import { STAGE } from "../../common/custom-parameters.js";
import {
  buildGoServiceServerless,
  type GoHttpEndpoint,
} from "../../common/go-service.js";

/**
 * Stack de DynamoDB del repo de infraestructura
 * (ind-hub-inf-aws-sls-pri-gh/ddb/). Publica un output `<Tabla>Name` y otro
 * `<Tabla>Arn` por tabla — ver `buildTableOutputs` en ese archivo.
 */
const DDB_STACK = `indomito-hub-infra-ddb-${STAGE}`;

/**
 * ARN de la tabla `catalogos`, para las politicas IAM.
 *
 * Se referencia como string y no como objeto intrinseco (`Fn::ImportValue`)
 * porque los builders de `aws/policies/dynamodb.ts` tipan `tableArn: string` y
 * concatenan `/index/*` para cubrir los GSI. Un objeto ahi produciria
 * `"[object Object]/index/*"`.
 */
const CATALOGS_TABLE_ARN = `\${cf:${DDB_STACK}.CatalogosTableArn}`;

/**
 * Nombre de la tabla `catalogos`, que las funciones leen de
 * `CATALOGS_TABLE_NAME` (ver `functions/config.go`).
 *
 * Sale del output de CloudFormation y no de SSM porque no es un secreto sino
 * una referencia entre stacks: el nombre lo decide el stack que crea la tabla,
 * y tomarlo de ahi hace imposible que este servicio apunte a una tabla que no
 * existe.
 */
const CATALOGS_TABLE_NAME = `\${cf:${DDB_STACK}.CatalogosTableName}`;
const CONFIGURATIONS_TABLE_ARN = `\${cf:${DDB_STACK}.ConfiguracionesTableArn}`;
const CONFIGURATIONS_TABLE_NAME = `\${cf:${DDB_STACK}.ConfiguracionesTableName}`;

/**
 * Token de la API BDE del Banco Central. El fallback vacío mantiene operativo
 * mindicador.cl hasta que infraestructura publique el SecureString del stage.
 */
const BCCH_API_TOKEN = `\${ssm:/indomito/${STAGE}/rates/bcch-api-token, ''}`;

/**
 * Endpoints del servicio. Es la unica declaracion TypeScript de su superficie
 * HTTP; la contraparte en Go son las rutas de `functions/routes.go`, y el test
 * de consistencia de ese paquete comprueba que ambas coincidan.
 *
 * Ambos requieren el authorizer compartido. Aunque los datos sean catálogos,
 * forman parte del portal administrativo y mantienen una superficie uniforme:
 * salvo el login, toda ruta del producto exige una sesión válida.
 *
 * Memoria y timeout: 128 MB alcanza para ambos, que no procesan volumen. El
 * timeout si difiere. Los catalogos resuelven una sola consulta a DynamoDB y
 * cierran en 6 s. Las tasas hacen dos consultas externas en paralelo con hasta
 * 3 intentos y 10 s de limite cada uno, mas la escritura del snapshot de
 * respaldo: 30 s cubren el peor caso sin cortar antes que la politica de
 * reintentos, que es lo que dejaria al endpoint sin poder entregar el respaldo.
 */
const endpoints: GoHttpEndpoint[] = [
  {
    name: "fn-obtener-configuracion-tributaria-v1",
    method: "GET",
    path: "/configuracion/tributaria",
    public: false,
    memorySize: 128,
    timeout: 6,
    description: "Obtiene los porcentajes tributarios vigentes",
    policies: [dynamodbReadPolicy(CONFIGURATIONS_TABLE_ARN)],
  },
  {
    name: "fn-actualizar-configuracion-tributaria-v1",
    method: "PUT",
    path: "/configuracion/tributaria",
    public: false,
    memorySize: 128,
    timeout: 6,
    description: "Actualiza los porcentajes de IVA y retencion de tripulacion",
    policies: [dynamodbCrudPolicy(CONFIGURATIONS_TABLE_ARN)],
  },
  {
    name: "fn-obtener-catalogos-v1",
    method: "GET",
    path: "/catalogos",
    public: false,
    memorySize: 128,
    timeout: 6,
    description:
      "Catalogos del formulario de programa y parametros de politica",
    // Solo lectura: los catalogos y los parametros los administra otro proceso.
    policies: [dynamodbReadPolicy(CATALOGS_TABLE_ARN)],
  },
  {
    name: "fn-obtener-tasas-cambio-v1",
    method: "GET",
    path: "/tasas-cambio",
    public: false,
    memorySize: 128,
    timeout: 30,
    description:
      "Tasas de cambio de USD y BRL a CLP, con respaldo por snapshot",
    // CRUD y no solo lectura por una unica razon: esta funcion escribe el
    // snapshot de respaldo (pk = RATES, sk = LATEST) que le permite responder
    // cuando la fuente externa no contesta.
    policies: [dynamodbCrudPolicy(CATALOGS_TABLE_ARN)],
  },
];

const config = buildGoServiceServerless(ApiServices.Catalog, endpoints, {
  env: {
    CATALOGS_TABLE_NAME,
    CONFIGURATIONS_TABLE_NAME,
    BCCH_API_TOKEN,
  },
});

module.exports = config;
