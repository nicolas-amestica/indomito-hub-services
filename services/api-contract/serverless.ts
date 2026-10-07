import {
  dynamodbCrudPolicy,
  dynamodbReadPolicy,
} from "../../aws/policies/dynamodb.js";
import { ApiServices } from "../../common/api-services.js";
import { STAGE, REGION } from "../../common/custom-parameters.js";
import { ssmReadPolicy } from "../../aws/policies/ssm.js";
import {
  buildGoServiceServerless,
  type GoHttpEndpoint,
} from "../../common/go-service.js";

const DDB_STACK = `indomito-hub-infra-ddb-${STAGE}`;
const TABLE_ARN = `\${cf:${DDB_STACK}.ProgramasTableArn}`;
const TABLE_NAME = `\${cf:${DDB_STACK}.ProgramasTableName}`;
// La integración financiera se habilita primero en DEV; no modifica el flujo PRD.
const paymentPolicies = STAGE === "dev" ? [
  { Effect: "Allow" as const, Action: ["dynamodb:PutItem"], Resource: `\${cf:${DDB_STACK}.PagosTableArn}` },
  ...["portal-url", "lookup-secret"].map((name) => ssmReadPolicy({ "Fn::Sub": `arn:\${AWS::Partition}:ssm:${REGION}:\${AWS::AccountId}:parameter/indomito/${STAGE}/payments/${name}` })),
] : [];
const CATALOGS_TABLE_ARN = `\${cf:${DDB_STACK}.CatalogosTableArn}`;
const CATALOGS_TABLE_NAME = `\${cf:${DDB_STACK}.CatalogosTableName}`;
const S3_STACK = `indomito-hub-infra-s3-${STAGE}`;
const DOCUMENTS_BUCKET_ARN = `\${cf:${S3_STACK}.MediaBucketArn}`;
const DOCUMENTS_BUCKET_NAME = `\${cf:${S3_STACK}.MediaBucketName}`;
const documentReadPolicy = {
  Effect: "Allow" as const,
  Action: ["s3:GetObject"],
  Resource: `${DOCUMENTS_BUCKET_ARN}/contracts/*`,
};
const documentWritePolicy = {
  Effect: "Allow" as const,
  Action: ["s3:PutObject"],
  Resource: `${DOCUMENTS_BUCKET_ARN}/contracts/*`,
};

const endpoints: GoHttpEndpoint[] = [
  {
    name: "fn-crear-contrato-v1",
    method: "POST",
    path: "/contratos",
    public: false,
    memorySize: 256,
    timeout: 10,
    description: "Crea un contrato en estado borrador",
    policies: [dynamodbCrudPolicy(TABLE_ARN)],
  },
  {
    name: "fn-listar-contratos-v1",
    method: "GET",
    path: "/contratos",
    public: false,
    memorySize: 128,
    timeout: 8,
    description: "Lista contratos por periodo",
    policies: [dynamodbReadPolicy(TABLE_ARN)],
  },
  {
    name: "fn-obtener-contrato-v1",
    method: "GET",
    path: "/contratos/{id-contrato}",
    public: false,
    memorySize: 128,
    timeout: 8,
    description: "Obtiene un contrato",
    policies: [dynamodbReadPolicy(TABLE_ARN)],
  },
  {
    name: "fn-actualizar-contrato-v1",
    method: "PUT",
    path: "/contratos/{id-contrato}",
    public: false,
    memorySize: 512,
    timeout: 20,
    description:
      "Actualiza un contrato no aprobado y almacena su PDF al aprobar",
    policies: [dynamodbCrudPolicy(TABLE_ARN), documentWritePolicy, ...paymentPolicies],
  },
  {
    name: "fn-generar-contrato-pdf-v1",
    method: "POST",
    path: "/contratos:pdf",
    public: false,
    memorySize: 512,
    timeout: 20,
    description: "Genera el PDF del contrato en memoria",
    policies: [],
  },
  {
    name: "fn-obtener-pdf-contrato-v1",
    method: "GET",
    path: "/contratos/{id-contrato}/pdf",
    public: false,
    memorySize: 128,
    timeout: 8,
    description: "Entrega acceso temporal al PDF aprobado",
    policies: [dynamodbReadPolicy(TABLE_ARN), documentReadPolicy],
  },
  {
    name: "fn-obtener-configuracion-contrato-v1",
    method: "GET",
    path: "/contratos:configuracion",
    public: false,
    memorySize: 128,
    timeout: 8,
    description: "Obtiene la configuracion inicial del formulario por scope",
    policies: [dynamodbReadPolicy(CATALOGS_TABLE_ARN)],
  },
  {
    name: "fn-crear-anexo-contrato-v1",
    method: "POST",
    path: "/contratos/{id-contrato}/anexos",
    public: false,
    memorySize: 256,
    timeout: 10,
    description: "Crea un anexo de fechas y servicios sobre un contrato aprobado",
    policies: [dynamodbCrudPolicy(TABLE_ARN)],
  },
  {
    name: "fn-listar-anexos-contrato-v1",
    method: "GET",
    path: "/contratos/{id-contrato}/anexos",
    public: false,
    memorySize: 128,
    timeout: 8,
    description: "Lista los anexos contractuales sin consultar otras particiones",
    policies: [dynamodbReadPolicy(TABLE_ARN)],
  },
  {
    name: "fn-aprobar-anexo-contrato-v1",
    method: "POST",
    path: "/contratos/{id-contrato}/anexos/{id-anexo}/aprobacion",
    public: false,
    memorySize: 512,
    timeout: 20,
    description: "Aprueba y almacena el PDF inmutable de un anexo contractual",
    policies: [dynamodbCrudPolicy(TABLE_ARN), documentWritePolicy, ...paymentPolicies],
  },
  {
    name: "fn-obtener-pdf-anexo-contrato-v1",
    method: "GET",
    path: "/contratos/{id-contrato}/anexos/{id-anexo}/pdf",
    public: false,
    memorySize: 128,
    timeout: 8,
    description: "Entrega acceso temporal al PDF aprobado de un anexo",
    policies: [dynamodbReadPolicy(TABLE_ARN), documentReadPolicy],
  },
  { name: "fn-preparar-carga-contrato-firmado-v1", method: "POST", path: "/contratos/{id-contrato}/documentos-firmados:carga", public: false, memorySize: 128, timeout: 8, description: "Prepara la carga idempotente del contrato firmado", policies: [dynamodbCrudPolicy(TABLE_ARN), documentWritePolicy] },
  { name: "fn-confirmar-carga-contrato-firmado-v1", method: "POST", path: "/contratos/{id-contrato}/documentos-firmados:confirmar", public: false, memorySize: 256, timeout: 29, description: "Verifica y publica una version firmada", policies: [dynamodbCrudPolicy(TABLE_ARN), documentReadPolicy] },
  { name: "fn-listar-contratos-firmados-v1", method: "GET", path: "/contratos/{id-contrato}/documentos-firmados", public: false, memorySize: 128, timeout: 8, description: "Lista el historial firmado", policies: [dynamodbReadPolicy(TABLE_ARN)] },
  { name: "fn-obtener-contrato-firmado-v1", method: "GET", path: "/contratos/{id-contrato}/documento-firmado/pdf", public: false, memorySize: 128, timeout: 8, description: "Entrega el contrato firmado vigente", policies: [dynamodbReadPolicy(TABLE_ARN), documentReadPolicy] },
  { name: "fn-obtener-version-contrato-firmado-v1", method: "GET", path: "/contratos/{id-contrato}/documentos-firmados/{id-documento-firmado}/pdf", public: false, memorySize: 128, timeout: 8, description: "Entrega una version historica firmada", policies: [dynamodbReadPolicy(TABLE_ARN), documentReadPolicy] },
];

module.exports = buildGoServiceServerless(ApiServices.Contract, endpoints, {
  env: {
    PROGRAMS_TABLE_NAME: TABLE_NAME,
    CATALOGS_TABLE_NAME,
    DOCUMENTS_BUCKET_NAME,
    ...(STAGE === "dev" ? { PAYMENTS_TABLE_NAME: `\${cf:${DDB_STACK}.PagosTableName}` } : {}),
  },
});
