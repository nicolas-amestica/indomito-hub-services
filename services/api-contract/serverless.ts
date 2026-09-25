import { dynamodbCrudPolicy, dynamodbReadPolicy } from '../../aws/policies/dynamodb.js';
import { ApiServices } from '../../common/api-services.js';
import { STAGE } from '../../common/custom-parameters.js';
import { buildGoServiceServerless, type GoHttpEndpoint } from '../../common/go-service.js';

const DDB_STACK = `indomito-hub-infra-ddb-${STAGE}`;
const TABLE_ARN = `\${cf:${DDB_STACK}.ProgramasTableArn}`;
const TABLE_NAME = `\${cf:${DDB_STACK}.ProgramasTableName}`;
const CATALOGS_TABLE_ARN = `\${cf:${DDB_STACK}.CatalogosTableArn}`;
const CATALOGS_TABLE_NAME = `\${cf:${DDB_STACK}.CatalogosTableName}`;

const endpoints: GoHttpEndpoint[] = [
  { name: 'fn-crear-contrato-v1', method: 'POST', path: '/contratos', public: false, memorySize: 256, timeout: 10, description: 'Crea un contrato en estado borrador', policies: [dynamodbCrudPolicy(TABLE_ARN)] },
  { name: 'fn-listar-contratos-v1', method: 'GET', path: '/contratos', public: false, memorySize: 128, timeout: 8, description: 'Lista contratos por periodo', policies: [dynamodbReadPolicy(TABLE_ARN)] },
  { name: 'fn-obtener-contrato-v1', method: 'GET', path: '/contratos/{id-contrato}', public: false, memorySize: 128, timeout: 8, description: 'Obtiene un contrato', policies: [dynamodbReadPolicy(TABLE_ARN)] },
  { name: 'fn-actualizar-contrato-v1', method: 'PUT', path: '/contratos/{id-contrato}', public: false, memorySize: 256, timeout: 10, description: 'Actualiza un contrato no aprobado', policies: [dynamodbCrudPolicy(TABLE_ARN)] },
  { name: 'fn-generar-contrato-pdf-v1', method: 'POST', path: '/contratos:pdf', public: false, memorySize: 512, timeout: 20, description: 'Genera el PDF del contrato en memoria', policies: [] },
  { name: 'fn-obtener-configuracion-contrato-v1', method: 'GET', path: '/contratos:configuracion', public: false, memorySize: 128, timeout: 8, description: 'Obtiene la configuracion inicial del formulario por scope', policies: [dynamodbReadPolicy(CATALOGS_TABLE_ARN)] },
];

module.exports = buildGoServiceServerless(ApiServices.Contract, endpoints, { env: { PROGRAMS_TABLE_NAME: TABLE_NAME, CATALOGS_TABLE_NAME } });
