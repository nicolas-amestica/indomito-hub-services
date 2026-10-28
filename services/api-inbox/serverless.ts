import { dynamodbCrudPolicy } from "../../aws/policies/dynamodb.js";
import { ssmReadPolicy } from "../../aws/policies/ssm.js";
import { s3ObjectPolicy } from "../../aws/policies/s3.js";
import { ApiServices } from "../../common/api-services.js";
import { REGION, STAGE } from "../../common/custom-parameters.js";
import { buildGoServiceServerless, type GoHttpEndpoint } from "../../common/go-service.js";
import type { AWS } from "@serverless/typescript";

const DDB_STACK = `indomito-hub-infra-ddb-${STAGE}`;
const S3_STACK = `indomito-hub-infra-s3-${STAGE}`;
const WEBSOCKET_STACK = `indomito-hub-infra-websocket-${STAGE}`;
const TABLE_NAME = `\${cf:${DDB_STACK}.ConversacionesTableName}`;
const TABLE_ARN = `\${cf:${DDB_STACK}.ConversacionesTableArn}`;
const TABLE_STREAM_ARN = `\${cf:${DDB_STACK}.ConversacionesTableStreamArn}`;
const INBOX_BUCKET_NAME = `\${cf:${S3_STACK}.InboxTemporaryBucketName}`;
const INBOX_BUCKET_OBJECTS_ARN = `\${cf:${S3_STACK}.InboxTemporaryBucketArn}/*`;
const INBOX_ASSETS_BUCKET_ARN = `\${cf:${S3_STACK}.InboxAssetsBucketArn}`;
const INBOX_ASSETS_BUCKET_NAME = `\${cf:${S3_STACK}.InboxAssetsBucketName}`;
const INBOX_ASSETS_BUCKET_OBJECTS_ARN = `${INBOX_ASSETS_BUCKET_ARN}/*`;
const INBOX_WEBSOCKET_URL = `\${cf:${WEBSOCKET_STACK}.WebSocketUrl}`;
const INBOX_WEBSOCKET_API_ID = `\${cf:${WEBSOCKET_STACK}.WebSocketApiId}`;
const INBOX_WEBSOCKET_EXECUTION_ARN = `\${cf:${WEBSOCKET_STACK}.WebSocketApiExecutionArn}`;
const INBOX_WEBSOCKET_MANAGEMENT_ENDPOINT = `\${cf:${WEBSOCKET_STACK}.WebSocketManagementEndpoint}`;
const INBOX_PARAMETER_ARN = {
  "Fn::Sub": `arn:\${AWS::Partition}:ssm:${REGION}:\${AWS::AccountId}:parameter/indomito/${STAGE}/inbox/*`,
};
const GOOGLE_CALENDAR_OAUTH_PARAMETER_ARN = {
  "Fn::Sub": `arn:\${AWS::Partition}:ssm:${REGION}:\${AWS::AccountId}:parameter/indomito/${STAGE}/google/calendar-oauth`,
};
const PAYMENTS_RECEIPT_SMTP_PARAMETER_ARN = {
  "Fn::Sub": `arn:\${AWS::Partition}:ssm:${REGION}:\${AWS::AccountId}:parameter/indomito/${STAGE}/payments/receipt-smtp`,
};
const INBOX_OPERATIONAL_ALERTS_TOPIC = { Ref: "InboxOperationalAlerts" };
const FREE_TIER_READ_ROLE_ARN = "arn:aws:iam::691725736887:role/IndomitoHubFreeTierReadRole";

const endpoints: GoHttpEndpoint[] = [
  {
    name: "fn-obtener-configuracion-agente-v1",
    method: "GET",
    path: "/configuracion-agente",
    public: false, memorySize: 256, timeout: 12, description: "Obtiene borrador, version publicada e historial del agente IA",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-obtener-metricas-operacionales-v1",
    method: "GET",
    path: "/configuracion-agente/metricas",
    public: false, memorySize: 256, timeout: 20, description: "Obtiene consumo de tokens, costo AWS y estado del Free Tier con cache diario",
    policies: [
      dynamodbCrudPolicy(TABLE_ARN),
      ssmReadPolicy(INBOX_PARAMETER_ARN),
      { Effect: "Allow", Action: ["ce:GetCostAndUsage"], Resource: "*" },
      { Effect: "Allow", Action: ["sts:AssumeRole"], Resource: FREE_TIER_READ_ROLE_ARN },
    ],
  },
  {
    name: "fn-guardar-borrador-agente-v1",
    method: "PUT",
    path: "/configuracion-agente/borrador",
    public: false, memorySize: 256, timeout: 12, description: "Guarda un borrador validado de configuracion del agente IA",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-publicar-configuracion-agente-v1",
    method: "POST",
    path: "/configuracion-agente/publicaciones",
    public: false, memorySize: 256, timeout: 12, description: "Publica de forma atomica la configuracion del agente IA",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-restaurar-configuracion-agente-v1",
    method: "POST",
    path: "/configuracion-agente/restauraciones",
    public: false, memorySize: 256, timeout: 12, description: "Restaura una version historica del agente IA",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-preparar-activo-agente-v1",
    method: "POST",
    path: "/configuracion-agente/programas/cargas",
    public: false, memorySize: 256, timeout: 12, description: "Firma una carga PDF privada para un programa del agente IA",
    policies: [dynamodbCrudPolicy(TABLE_ARN), s3ObjectPolicy(INBOX_ASSETS_BUCKET_OBJECTS_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-descargar-activo-agente-v1",
    method: "GET",
    path: "/configuracion-agente/programas/descargas",
    public: false, memorySize: 128, timeout: 12, description: "Firma la descarga privada de un PDF del agente IA",
    policies: [s3ObjectPolicy(INBOX_ASSETS_BUCKET_OBJECTS_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-obtener-conversaciones-v1",
    method: "GET",
    path: "/conversaciones",
    public: false,
    memorySize: 512,
    timeout: 29,
    description: "Materializa y pagina la bandeja Inbox sin operaciones Scan",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-obtener-conversacion-v1",
    method: "GET",
    path: "/conversaciones/{telefono}",
    public: false,
    memorySize: 256,
    timeout: 12,
    description: "Obtiene historial, watermark y envios no terminales de una conversacion",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-enviar-respuesta-v1",
    method: "POST",
    path: "/conversaciones/{telefono}/respuestas",
    public: false,
    memorySize: 256,
    timeout: 29,
    description: "Envia una respuesta idempotente por WhatsApp",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-listar-plantillas-v1",
    method: "GET",
    path: "/plantillas-whatsapp",
    public: false, memorySize: 256, timeout: 12, description: "Lista plantillas aprobadas de WhatsApp",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-enviar-plantilla-v1",
    method: "POST",
    path: "/conversaciones/{telefono}/plantillas",
    public: false, memorySize: 256, timeout: 29, description: "Envia una plantilla aprobada de WhatsApp",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-enviar-reaccion-v1",
    method: "PUT",
    path: "/conversaciones/{telefono}/reacciones",
    public: false, memorySize: 256, timeout: 29, description: "Agrega o elimina una reaccion de WhatsApp",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-enviar-mensaje-enriquecido-v1",
    method: "POST",
    path: "/conversaciones/{telefono}/mensajes-enriquecidos",
    public: false, memorySize: 256, timeout: 29, description: "Envia ubicaciones o contactos por WhatsApp",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-preparar-adjuntos-v1",
    method: "POST",
    path: "/conversaciones/{telefono}/adjuntos/preparar",
    public: false, memorySize: 256, timeout: 12, description: "Reserva y firma cargas temporales de Inbox",
    policies: [dynamodbCrudPolicy(TABLE_ARN), s3ObjectPolicy(INBOX_BUCKET_OBJECTS_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-enviar-adjunto-v1",
    method: "POST",
    path: "/conversaciones/{telefono}/adjuntos",
    public: false, memorySize: 512, timeout: 29, description: "Valida y envia un adjunto Inbox por WhatsApp",
    policies: [dynamodbCrudPolicy(TABLE_ARN), s3ObjectPolicy(INBOX_BUCKET_OBJECTS_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-actualizar-contacto-v1",
    method: "PATCH",
    path: "/conversaciones/{telefono}/contacto",
    public: false, memorySize: 256, timeout: 12, description: "Actualiza el alias de una conversacion",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-marcar-no-leida-v1",
    method: "PATCH",
    path: "/conversaciones/{telefono}/estado-lectura",
    public: false, memorySize: 256, timeout: 12, description: "Marca una conversacion como no leida",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-reactivar-agente-v1",
    method: "PATCH",
    path: "/conversaciones/{telefono}/estado-agente",
    public: false, memorySize: 256, timeout: 12, description: "Reactiva explicitamente el agente IA de una conversacion",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-eliminar-conversacion-v1",
    method: "DELETE",
    path: "/conversaciones/{telefono}",
    public: false, memorySize: 256, timeout: 12, description: "Elimina logicamente una conversacion Inbox",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-iniciar-marcar-leidas-v1",
    method: "PATCH",
    path: "/conversaciones/acciones/marcar-leidas",
    public: false, memorySize: 256, timeout: 12, description: "Inicia el marcado masivo de conversaciones como leidas",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-iniciar-recalcular-no-leidas-v1",
    method: "PATCH",
    path: "/conversaciones/acciones/recalcular-no-leidas",
    public: false, memorySize: 256, timeout: 12, description: "Inicia el recalculo masivo de no leidas",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-crear-suscripcion-v1",
    method: "GET",
    path: "/conversaciones/suscripcion",
    public: false, memorySize: 256, timeout: 12, description: "Emite un ticket efimero para la suscripcion WebSocket",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-obtener-envio-v1",
    method: "GET",
    path: "/envios-inbox/{clientMessageId}",
    public: false, memorySize: 256, timeout: 12, description: "Consulta un envio Inbox",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-reintentar-envio-v1",
    method: "POST",
    path: "/envios-inbox/{clientMessageId}/reintentar",
    public: false, memorySize: 256, timeout: 29, description: "Reintenta una vez un envio Inbox",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-resolver-envio-v1",
    method: "PATCH",
    path: "/envios-inbox/{clientMessageId}/resolucion",
    public: false, memorySize: 256, timeout: 12, description: "Resuelve administrativamente un envio incierto",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-obtener-operacion-activa-v1",
    method: "GET",
    path: "/operaciones-inbox",
    public: false, memorySize: 256, timeout: 12, description: "Consulta la operacion masiva activa",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-obtener-operacion-v1",
    method: "GET",
    path: "/operaciones-inbox/{operationId}",
    public: false, memorySize: 256, timeout: 12, description: "Consulta una operacion masiva por identificador",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-verificar-webhook-whatsapp-v1",
    method: "GET",
    path: "/webhooks/whatsapp",
    public: true, memorySize: 128, timeout: 6, description: "Verifica el callback de WhatsApp",
    policies: [ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-recibir-webhook-whatsapp-v1",
    method: "POST",
    path: "/webhooks/whatsapp",
    public: true, memorySize: 256, timeout: 12, description: "Autentica y reserva eventos entrantes de WhatsApp",
    policies: [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN)],
  },
  {
    name: "fn-despachar-trabajo-inbox-v1",
    memorySize: 128,
    timeout: 15,
    description: "Entrega trabajo Inbox durable desde DynamoDB Streams hacia SQS",
    env: {
      INBOX_INBOUND_QUEUE_URL: { Ref: "InboxInboundQueue" } as unknown as string,
      INBOX_WORKFLOW_QUEUE_URL: { Ref: "InboxWorkflowQueue" } as unknown as string,
      INBOX_REALTIME_QUEUE_URL: { Ref: "InboxRealtimeQueue" } as unknown as string,
      INBOX_AGENT_QUEUE_URL: { Ref: "InboxAgentQueue" } as unknown as string,
    },
    events: [{
      stream: {
        type: "dynamodb",
        arn: TABLE_STREAM_ARN,
        startingPosition: "LATEST",
        batchSize: 10,
        maximumRetryAttempts: 5,
        maximumRecordAgeInSeconds: 3600,
        bisectBatchOnFunctionError: true,
        functionResponseType: "ReportBatchItemFailures",
        filterPatterns: [{ eventName: ["INSERT"], dynamodb: { NewImage: { pk: { S: [{ prefix: "WORK_DUE#" }] }, state: { S: ["pending"] } } } }],
      },
    }],
    policies: [
      { Effect: "Allow", Action: ["dynamodb:DescribeStream", "dynamodb:GetRecords", "dynamodb:GetShardIterator", "dynamodb:ListStreams"], Resource: TABLE_STREAM_ARN },
      { Effect: "Allow", Action: ["sqs:SendMessage"], Resource: [{ "Fn::GetAtt": ["InboxInboundQueue", "Arn"] }, { "Fn::GetAtt": ["InboxWorkflowQueue", "Arn"] }, { "Fn::GetAtt": ["InboxRealtimeQueue", "Arn"] }, { "Fn::GetAtt": ["InboxAgentQueue", "Arn"] }] as unknown as string[] },
    ],
  },
  {
    name: "fn-procesar-mensaje-entrante-v1",
    memorySize: 256,
    timeout: 30,
    description: "Materializa mensajes Meta verificados en Inbox",
    events: [{ sqs: { arn: { "Fn::GetAtt": ["InboxInboundQueue", "Arn"] }, batchSize: 5, functionResponseType: "ReportBatchItemFailures" } }],
    policies: [
      dynamodbCrudPolicy(TABLE_ARN),
      s3ObjectPolicy(INBOX_BUCKET_OBJECTS_ARN),
      ssmReadPolicy(INBOX_PARAMETER_ARN),
      { Effect: "Allow", Action: ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes", "sqs:ChangeMessageVisibility"], Resource: { "Fn::GetAtt": ["InboxInboundQueue", "Arn"] } as unknown as string },
    ],
  },
  {
    name: "fn-procesar-agente-inbox-v1",
    memorySize: 512,
    timeout: 180,
    description: "Procesa el agente Gemini, audio y controles de frecuencia de Inbox",
    events: [{ sqs: { arn: { "Fn::GetAtt": ["InboxAgentQueue", "Arn"] }, batchSize: 1, functionResponseType: "ReportBatchItemFailures" } }],
    policies: [
      dynamodbCrudPolicy(TABLE_ARN), s3ObjectPolicy(INBOX_BUCKET_OBJECTS_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN), ssmReadPolicy(GOOGLE_CALENDAR_OAUTH_PARAMETER_ARN), ssmReadPolicy(PAYMENTS_RECEIPT_SMTP_PARAMETER_ARN),
      { Effect: "Allow", Action: ["s3:ListBucket"], Resource: INBOX_ASSETS_BUCKET_ARN },
      { Effect: "Allow", Action: ["s3:GetObject"], Resource: INBOX_ASSETS_BUCKET_OBJECTS_ARN },
      { Effect: "Allow", Action: ["transcribe:StartTranscriptionJob", "transcribe:GetTranscriptionJob"], Resource: "*" },
      { Effect: "Allow", Action: ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes", "sqs:ChangeMessageVisibility"], Resource: { "Fn::GetAtt": ["InboxAgentQueue", "Arn"] } as unknown as string },
    ],
  },
  {
    name: "fn-conectar-websocket-v1",
    memorySize: 128,
    timeout: 10,
    description: "Consume un ticket efimero y registra una conexion Inbox",
    policies: [dynamodbCrudPolicy(TABLE_ARN)],
  },
  {
    name: "fn-desconectar-websocket-v1",
    memorySize: 128,
    timeout: 10,
    description: "Elimina una conexion y su proyeccion de suscripcion Inbox",
    policies: [dynamodbCrudPolicy(TABLE_ARN)],
  },
  {
    name: "fn-publicar-evento-inbox-v1",
    memorySize: 256,
    timeout: 30,
    description: "Publica eventos Inbox a suscriptores WebSocket activos",
    env: { INBOX_WEBSOCKET_MANAGEMENT_ENDPOINT },
    events: [{ sqs: { arn: { "Fn::GetAtt": ["InboxRealtimeQueue", "Arn"] }, batchSize: 5, functionResponseType: "ReportBatchItemFailures" } }],
    policies: [
      dynamodbCrudPolicy(TABLE_ARN),
      { Effect: "Allow", Action: ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes", "sqs:ChangeMessageVisibility"], Resource: { "Fn::GetAtt": ["InboxRealtimeQueue", "Arn"] } as unknown as string },
      { Effect: "Allow", Action: ["execute-api:ManageConnections"], Resource: `${INBOX_WEBSOCKET_EXECUTION_ARN}/*/@connections/*` },
    ],
  },
  {
    name: "fn-procesar-operacion-masiva-v1",
    memorySize: 512,
    timeout: 900,
    description: "Aplica operaciones masivas Inbox sobre un cutoff consistente",
    events: [{ sqs: { arn: { "Fn::GetAtt": ["InboxWorkflowQueue", "Arn"] }, batchSize: 1, functionResponseType: "ReportBatchItemFailures" } }],
    policies: [
      dynamodbCrudPolicy(TABLE_ARN),
      { Effect: "Allow", Action: ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes", "sqs:ChangeMessageVisibility"], Resource: { "Fn::GetAtt": ["InboxWorkflowQueue", "Arn"] } as unknown as string },
    ],
  },
];

const serverlessConfiguration = buildGoServiceServerless(ApiServices.Inbox, endpoints, {
  env: {
    CONVERSATIONS_TABLE_NAME: TABLE_NAME,
    INBOX_CURSOR_SECRET_PARAM: `/indomito/${STAGE}/inbox/cursor-secret`,
    INBOX_RUNTIME_CONFIG_PARAM: `/indomito/${STAGE}/inbox/runtime-config`,
    INBOX_WHATSAPP_CONFIG_PARAM: `/indomito/${STAGE}/inbox/whatsapp-config`,
    INBOX_GEMINI_CONFIG_PARAM: `/indomito/${STAGE}/inbox/gemini-config`,
    INBOX_AGENT_CONFIG_PARAM: `/indomito/${STAGE}/inbox/agent-config`,
    INBOX_ASSET_CONFIG_PARAM: `/indomito/${STAGE}/inbox/asset-config`,
    INBOX_MEETING_CONFIG_PARAM: `/indomito/${STAGE}/inbox/meeting-config`,
    INBOX_DERIVATION_NOTIFICATION_CONFIG_PARAM: `/indomito/${STAGE}/inbox/derivation-notification-config`,
    INBOX_OPERATIONAL_METRICS_CONFIG_PARAM: `/indomito/${STAGE}/inbox/operational-metrics-config`,
    INBOX_TEMPORARY_BUCKET_NAME: INBOX_BUCKET_NAME,
    INBOX_ASSETS_BUCKET_NAME,
    INBOX_WEBSOCKET_URL,
  },
});

const existingResources = serverlessConfiguration.resources?.Resources ?? {};
type CfResources = NonNullable<NonNullable<AWS["resources"]>["Resources"]>;

const alarmProperties = (
  description: string,
  namespace: string,
  metricName: string,
  dimensions: unknown[],
  threshold: number,
  period = 300,
) => ({
  AlarmDescription: description,
  Namespace: namespace,
  MetricName: metricName,
  Statistic: "Maximum",
  Period: period,
  EvaluationPeriods: 2,
  DatapointsToAlarm: 2,
  Threshold: threshold,
  ComparisonOperator: "GreaterThanOrEqualToThreshold",
  TreatMissingData: "notBreaching",
  Dimensions: dimensions,
  AlarmActions: [INBOX_OPERATIONAL_ALERTS_TOPIC],
  OKActions: [INBOX_OPERATIONAL_ALERTS_TOPIC],
});

const lambdaDimensions = (functionName: string) => [{
  Name: "FunctionName",
  Value: `${ApiServices.Inbox}-${STAGE}-${functionName}`,
}];

const queueDimensions = (logicalID: string) => [{
  Name: "QueueName",
  Value: { "Fn::GetAtt": [logicalID, "QueueName"] },
}];

const dashboardBody = JSON.stringify({
  start: "-PT6H",
  periodOverride: "inherit",
  widgets: [
    {
      type: "metric", x: 0, y: 0, width: 12, height: 6,
      properties: {
        title: "Webhook y envios salientes", region: REGION, view: "timeSeries", period: 60, stat: "Sum",
        metrics: [
          ["AWS/Lambda", "Errors", "FunctionName", `${ApiServices.Inbox}-${STAGE}-fn-recibir-webhook-whatsapp-v1`, { label: "Webhook errors" }],
          [".", "Duration", ".", ".", { label: "Webhook duration max", stat: "Maximum", yAxis: "right" }],
          [".", "Errors", ".", `${ApiServices.Inbox}-${STAGE}-fn-enviar-respuesta-v1`, { label: "Outbound errors" }],
        ],
      },
    },
    {
      type: "metric", x: 12, y: 0, width: 12, height: 6,
      properties: {
        title: "Proveedores externos", region: REGION, view: "timeSeries", period: 60, stat: "Sum",
        metrics: [
          ["Indomito/Inbox", "GraphApiFailures", "Stage", STAGE],
          [".", "GeminiFailures", ".", "."],
          [".", "CalendarFailures", ".", "."],
          [".", "DerivationNotificationFailures", ".", "."],
        ],
      },
    },
    {
      type: "metric", x: 0, y: 6, width: 24, height: 7,
      properties: {
        title: "Edad de colas", region: REGION, view: "timeSeries", period: 60, stat: "Maximum",
        metrics: [
          ["AWS/SQS", "ApproximateAgeOfOldestMessage", "QueueName", "${InboundQueueName}", { label: "Inbound" }],
          [".", ".", ".", "${AgentQueueName}", { label: "Agent" }],
          [".", ".", ".", "${WorkflowQueueName}", { label: "Workflow" }],
          [".", ".", ".", "${RealtimeQueueName}", { label: "Realtime" }],
        ],
      },
    },
    {
      type: "metric", x: 0, y: 13, width: 12, height: 6,
      properties: {
        title: "Mensajes en DLQ", region: REGION, view: "singleValue", period: 60, stat: "Maximum",
        metrics: [
          ["AWS/SQS", "ApproximateNumberOfMessagesVisible", "QueueName", "${InboundDlqName}", { label: "Inbound DLQ" }],
          [".", ".", ".", "${AgentDlqName}", { label: "Agent DLQ" }],
          [".", ".", ".", "${WorkflowDlqName}", { label: "Workflow DLQ" }],
          [".", ".", ".", "${RealtimeDlqName}", { label: "Realtime DLQ" }],
        ],
      },
    },
    {
      type: "metric", x: 12, y: 13, width: 12, height: 6,
      properties: {
        title: "Publicacion realtime", region: REGION, view: "timeSeries", period: 60, stat: "Sum",
        metrics: [
          ["AWS/Lambda", "Errors", "FunctionName", `${ApiServices.Inbox}-${STAGE}-fn-publicar-evento-inbox-v1`, { label: "Errors" }],
          [".", "Throttles", ".", ".", { label: "Throttles" }],
          [".", "Duration", ".", ".", { label: "Duration max", stat: "Maximum", yAxis: "right" }],
        ],
      },
    },
  ],
});

serverlessConfiguration.resources = {
  ...serverlessConfiguration.resources,
  Resources: {
    ...existingResources,
    InboxOperationalAlerts: {
      Type: "AWS::SNS::Topic",
      Properties: {
        TopicName: `ind-inbox-${STAGE}-operational-alerts`,
        DisplayName: `Inbox ${STAGE} operational alerts`,
      },
    },
    InboxOperationalAlertsPolicy: {
      Type: "AWS::SNS::TopicPolicy",
      Properties: {
        Topics: [INBOX_OPERATIONAL_ALERTS_TOPIC],
        PolicyDocument: {
          Version: "2012-10-17",
          Statement: [{
            Sid: "AllowCloudWatchAlarmsToPublish",
            Effect: "Allow",
            Principal: { Service: "cloudwatch.amazonaws.com" },
            Action: "sns:Publish",
            Resource: INBOX_OPERATIONAL_ALERTS_TOPIC,
            Condition: {
              StringEquals: { "AWS:SourceAccount": { Ref: "AWS::AccountId" } },
              ArnLike: { "AWS:SourceArn": { "Fn::Sub": "arn:${AWS::Partition}:cloudwatch:${AWS::Region}:${AWS::AccountId}:alarm:*" } },
            },
          }],
        },
      },
    },
    InboxOperationalDashboard: {
      Type: "AWS::CloudWatch::Dashboard",
      Properties: {
        DashboardName: `ind-inbox-${STAGE}-operations`,
        DashboardBody: {
          "Fn::Sub": [dashboardBody, {
            InboundQueueName: { "Fn::GetAtt": ["InboxInboundQueue", "QueueName"] },
            AgentQueueName: { "Fn::GetAtt": ["InboxAgentQueue", "QueueName"] },
            WorkflowQueueName: { "Fn::GetAtt": ["InboxWorkflowQueue", "QueueName"] },
            RealtimeQueueName: { "Fn::GetAtt": ["InboxRealtimeQueue", "QueueName"] },
            InboundDlqName: { "Fn::GetAtt": ["InboxInboundDeadLetterQueue", "QueueName"] },
            AgentDlqName: { "Fn::GetAtt": ["InboxAgentDeadLetterQueue", "QueueName"] },
            WorkflowDlqName: { "Fn::GetAtt": ["InboxWorkflowDeadLetterQueue", "QueueName"] },
            RealtimeDlqName: { "Fn::GetAtt": ["InboxRealtimeDeadLetterQueue", "QueueName"] },
          }],
        },
      },
    },
    InboxInboundDeadLetterQueue: {
      Type: "AWS::SQS::Queue",
      Properties: { MessageRetentionPeriod: 1209600, SqsManagedSseEnabled: true },
    },
    InboxInboundQueue: {
      Type: "AWS::SQS::Queue",
      Properties: {
        VisibilityTimeout: 180,
        MessageRetentionPeriod: 345600,
        SqsManagedSseEnabled: true,
        RedrivePolicy: { deadLetterTargetArn: { "Fn::GetAtt": ["InboxInboundDeadLetterQueue", "Arn"] }, maxReceiveCount: 5 },
      },
    },
    InboxWorkflowDeadLetterQueue: {
      Type: "AWS::SQS::Queue",
      Properties: { MessageRetentionPeriod: 1209600, SqsManagedSseEnabled: true },
    },
    InboxAgentDeadLetterQueue: {
      Type: "AWS::SQS::Queue",
      Properties: { MessageRetentionPeriod: 1209600, SqsManagedSseEnabled: true },
    },
    InboxAgentQueue: {
      Type: "AWS::SQS::Queue",
      Properties: { VisibilityTimeout: 210, MessageRetentionPeriod: 345600, SqsManagedSseEnabled: true, RedrivePolicy: { deadLetterTargetArn: { "Fn::GetAtt": ["InboxAgentDeadLetterQueue", "Arn"] }, maxReceiveCount: 5 } },
    },
    InboxWorkflowQueue: {
      Type: "AWS::SQS::Queue",
      Properties: {
        VisibilityTimeout: 960,
        MessageRetentionPeriod: 345600,
        SqsManagedSseEnabled: true,
        RedrivePolicy: { deadLetterTargetArn: { "Fn::GetAtt": ["InboxWorkflowDeadLetterQueue", "Arn"] }, maxReceiveCount: 5 },
      },
    },
    InboxRealtimeDeadLetterQueue: {
      Type: "AWS::SQS::Queue",
      Properties: { MessageRetentionPeriod: 1209600, SqsManagedSseEnabled: true },
    },
    InboxRealtimeQueue: {
      Type: "AWS::SQS::Queue",
      Properties: {
        VisibilityTimeout: 90,
        MessageRetentionPeriod: 345600,
        SqsManagedSseEnabled: true,
        RedrivePolicy: { deadLetterTargetArn: { "Fn::GetAtt": ["InboxRealtimeDeadLetterQueue", "Arn"] }, maxReceiveCount: 5 },
      },
    },
    InboxWorkflowDlqAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: { AlarmDescription: "Inbox workflow DLQ contains messages", Namespace: "AWS/SQS", MetricName: "ApproximateNumberOfMessagesVisible", Statistic: "Maximum", Period: 60, EvaluationPeriods: 1, Threshold: 1, ComparisonOperator: "GreaterThanOrEqualToThreshold", TreatMissingData: "notBreaching", Dimensions: [{ Name: "QueueName", Value: { "Fn::GetAtt": ["InboxWorkflowDeadLetterQueue", "QueueName"] } }], AlarmActions: [INBOX_OPERATIONAL_ALERTS_TOPIC], OKActions: [INBOX_OPERATIONAL_ALERTS_TOPIC] },
    },
    InboxAgentDlqAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: { AlarmDescription: "Inbox agent DLQ contains messages", Namespace: "AWS/SQS", MetricName: "ApproximateNumberOfMessagesVisible", Statistic: "Maximum", Period: 60, EvaluationPeriods: 1, Threshold: 1, ComparisonOperator: "GreaterThanOrEqualToThreshold", TreatMissingData: "notBreaching", Dimensions: [{ Name: "QueueName", Value: { "Fn::GetAtt": ["InboxAgentDeadLetterQueue", "QueueName"] } }], AlarmActions: [INBOX_OPERATIONAL_ALERTS_TOPIC], OKActions: [INBOX_OPERATIONAL_ALERTS_TOPIC] },
    },
    InboxInboundDlqAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: { AlarmDescription: "Inbox inbound DLQ contains messages", Namespace: "AWS/SQS", MetricName: "ApproximateNumberOfMessagesVisible", Statistic: "Maximum", Period: 60, EvaluationPeriods: 1, Threshold: 1, ComparisonOperator: "GreaterThanOrEqualToThreshold", TreatMissingData: "notBreaching", Dimensions: [{ Name: "QueueName", Value: { "Fn::GetAtt": ["InboxInboundDeadLetterQueue", "QueueName"] } }], AlarmActions: [INBOX_OPERATIONAL_ALERTS_TOPIC], OKActions: [INBOX_OPERATIONAL_ALERTS_TOPIC] },
    },
    InboxRealtimeDlqAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: { AlarmDescription: "Inbox realtime DLQ contains messages", Namespace: "AWS/SQS", MetricName: "ApproximateNumberOfMessagesVisible", Statistic: "Maximum", Period: 60, EvaluationPeriods: 1, Threshold: 1, ComparisonOperator: "GreaterThanOrEqualToThreshold", TreatMissingData: "notBreaching", Dimensions: [{ Name: "QueueName", Value: { "Fn::GetAtt": ["InboxRealtimeDeadLetterQueue", "QueueName"] } }], AlarmActions: [INBOX_OPERATIONAL_ALERTS_TOPIC], OKActions: [INBOX_OPERATIONAL_ALERTS_TOPIC] },
    },
    InboxWebhookErrorsAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: alarmProperties("Inbox WhatsApp webhook Lambda errors", "AWS/Lambda", "Errors", lambdaDimensions("fn-recibir-webhook-whatsapp-v1"), 1, 60),
    },
    InboxWebhookLatencyAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: alarmProperties("Inbox WhatsApp webhook maximum latency is at least 8 seconds", "AWS/Lambda", "Duration", lambdaDimensions("fn-recibir-webhook-whatsapp-v1"), 8_000, 60),
    },
    InboxOutboundSendErrorsAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: alarmProperties("Inbox outbound send Lambda errors", "AWS/Lambda", "Errors", lambdaDimensions("fn-enviar-respuesta-v1"), 1, 60),
    },
    InboxGraphApiFailuresAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: alarmProperties("Inbox Graph API failures", "Indomito/Inbox", "GraphApiFailures", [{ Name: "Stage", Value: STAGE }], 1, 60),
    },
    InboxGeminiFailuresAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: alarmProperties("Inbox Gemini failures", "Indomito/Inbox", "GeminiFailures", [{ Name: "Stage", Value: STAGE }], 1, 60),
    },
    InboxCalendarFailuresAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: alarmProperties("Inbox Google Calendar failures", "Indomito/Inbox", "CalendarFailures", [{ Name: "Stage", Value: STAGE }], 1, 60),
    },
    InboxDerivationNotificationFailuresAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: alarmProperties("Inbox derivation notification failures", "Indomito/Inbox", "DerivationNotificationFailures", [{ Name: "Stage", Value: STAGE }], 1, 60),
    },
    InboxRealtimeErrorsAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: alarmProperties("Inbox realtime publisher Lambda errors", "AWS/Lambda", "Errors", lambdaDimensions("fn-publicar-evento-inbox-v1"), 1, 60),
    },
    InboxInboundQueueAgeAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: alarmProperties("Inbox inbound oldest message is at least 2 minutes old", "AWS/SQS", "ApproximateAgeOfOldestMessage", queueDimensions("InboxInboundQueue"), 120, 60),
    },
    InboxAgentQueueAgeAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: alarmProperties("Inbox agent oldest message is at least 5 minutes old", "AWS/SQS", "ApproximateAgeOfOldestMessage", queueDimensions("InboxAgentQueue"), 300, 60),
    },
    InboxWorkflowQueueAgeAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: alarmProperties("Inbox workflow oldest message is at least 5 minutes old", "AWS/SQS", "ApproximateAgeOfOldestMessage", queueDimensions("InboxWorkflowQueue"), 300, 60),
    },
    InboxRealtimeQueueAgeAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: alarmProperties("Inbox realtime oldest message is at least 2 minutes old", "AWS/SQS", "ApproximateAgeOfOldestMessage", queueDimensions("InboxRealtimeQueue"), 120, 60),
    },
    InboxWebSocketConnectIntegration: {
      Type: "AWS::ApiGatewayV2::Integration",
      Properties: {
        ApiId: INBOX_WEBSOCKET_API_ID,
        IntegrationType: "AWS_PROXY",
        IntegrationMethod: "POST",
        IntegrationUri: { "Fn::Sub": ["arn:${AWS::Partition}:apigateway:${AWS::Region}:lambda:path/2015-03-31/functions/${FunctionArn}/invocations", { FunctionArn: { "Fn::GetAtt": ["FnDashconectarDashwebsocketDashv1LambdaFunction", "Arn"] } }] },
      },
    },
    InboxWebSocketDisconnectIntegration: {
      Type: "AWS::ApiGatewayV2::Integration",
      Properties: {
        ApiId: INBOX_WEBSOCKET_API_ID,
        IntegrationType: "AWS_PROXY",
        IntegrationMethod: "POST",
        IntegrationUri: { "Fn::Sub": ["arn:${AWS::Partition}:apigateway:${AWS::Region}:lambda:path/2015-03-31/functions/${FunctionArn}/invocations", { FunctionArn: { "Fn::GetAtt": ["FnDashdesconectarDashwebsocketDashv1LambdaFunction", "Arn"] } }] },
      },
    },
    InboxWebSocketConnectRoute: {
      Type: "AWS::ApiGatewayV2::Route",
      Properties: { ApiId: INBOX_WEBSOCKET_API_ID, RouteKey: "$connect", AuthorizationType: "NONE", Target: { "Fn::Join": ["/", ["integrations", { Ref: "InboxWebSocketConnectIntegration" }]] } },
    },
    InboxWebSocketDisconnectRoute: {
      Type: "AWS::ApiGatewayV2::Route",
      Properties: { ApiId: INBOX_WEBSOCKET_API_ID, RouteKey: "$disconnect", AuthorizationType: "NONE", Target: { "Fn::Join": ["/", ["integrations", { Ref: "InboxWebSocketDisconnectIntegration" }]] } },
    },
    InboxWebSocketConnectPermission: {
      Type: "AWS::Lambda::Permission",
      Properties: { Action: "lambda:InvokeFunction", FunctionName: { "Fn::GetAtt": ["FnDashconectarDashwebsocketDashv1LambdaFunction", "Arn"] }, Principal: "apigateway.amazonaws.com", SourceArn: `${INBOX_WEBSOCKET_EXECUTION_ARN}/*/$connect` },
    },
    InboxWebSocketDisconnectPermission: {
      Type: "AWS::Lambda::Permission",
      Properties: { Action: "lambda:InvokeFunction", FunctionName: { "Fn::GetAtt": ["FnDashdesconectarDashwebsocketDashv1LambdaFunction", "Arn"] }, Principal: "apigateway.amazonaws.com", SourceArn: `${INBOX_WEBSOCKET_EXECUTION_ARN}/*/$disconnect` },
    },
  } as unknown as CfResources,
  Outputs: {
    ...serverlessConfiguration.resources?.Outputs,
    InboxInboundQueueUrl: { Value: { Ref: "InboxInboundQueue" } },
    InboxWorkflowQueueUrl: { Value: { Ref: "InboxWorkflowQueue" } },
    InboxRealtimeQueueUrl: { Value: { Ref: "InboxRealtimeQueue" } },
    InboxAgentQueueUrl: { Value: { Ref: "InboxAgentQueue" } },
    InboxOperationalAlertsTopicArn: { Value: INBOX_OPERATIONAL_ALERTS_TOPIC },
  },
};

module.exports = serverlessConfiguration;
