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

const endpoints: GoHttpEndpoint[] = [
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
      dynamodbCrudPolicy(TABLE_ARN), s3ObjectPolicy(INBOX_BUCKET_OBJECTS_ARN), ssmReadPolicy(INBOX_PARAMETER_ARN), ssmReadPolicy(GOOGLE_CALENDAR_OAUTH_PARAMETER_ARN),
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
    INBOX_TEMPORARY_BUCKET_NAME: INBOX_BUCKET_NAME,
    INBOX_WEBSOCKET_URL,
  },
});

const existingResources = serverlessConfiguration.resources?.Resources ?? {};
type CfResources = NonNullable<NonNullable<AWS["resources"]>["Resources"]>;
serverlessConfiguration.resources = {
  ...serverlessConfiguration.resources,
  Resources: {
    ...existingResources,
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
      Properties: { AlarmDescription: "Inbox workflow DLQ contains messages", Namespace: "AWS/SQS", MetricName: "ApproximateNumberOfMessagesVisible", Statistic: "Maximum", Period: 60, EvaluationPeriods: 1, Threshold: 1, ComparisonOperator: "GreaterThanOrEqualToThreshold", TreatMissingData: "notBreaching", Dimensions: [{ Name: "QueueName", Value: { "Fn::GetAtt": ["InboxWorkflowDeadLetterQueue", "QueueName"] } }] },
    },
    InboxAgentDlqAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: { AlarmDescription: "Inbox agent DLQ contains messages", Namespace: "AWS/SQS", MetricName: "ApproximateNumberOfMessagesVisible", Statistic: "Maximum", Period: 60, EvaluationPeriods: 1, Threshold: 1, ComparisonOperator: "GreaterThanOrEqualToThreshold", TreatMissingData: "notBreaching", Dimensions: [{ Name: "QueueName", Value: { "Fn::GetAtt": ["InboxAgentDeadLetterQueue", "QueueName"] } }] },
    },
    InboxInboundDlqAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: { AlarmDescription: "Inbox inbound DLQ contains messages", Namespace: "AWS/SQS", MetricName: "ApproximateNumberOfMessagesVisible", Statistic: "Maximum", Period: 60, EvaluationPeriods: 1, Threshold: 1, ComparisonOperator: "GreaterThanOrEqualToThreshold", TreatMissingData: "notBreaching", Dimensions: [{ Name: "QueueName", Value: { "Fn::GetAtt": ["InboxInboundDeadLetterQueue", "QueueName"] } }] },
    },
    InboxRealtimeDlqAlarm: {
      Type: "AWS::CloudWatch::Alarm",
      Properties: { AlarmDescription: "Inbox realtime DLQ contains messages", Namespace: "AWS/SQS", MetricName: "ApproximateNumberOfMessagesVisible", Statistic: "Maximum", Period: 60, EvaluationPeriods: 1, Threshold: 1, ComparisonOperator: "GreaterThanOrEqualToThreshold", TreatMissingData: "notBreaching", Dimensions: [{ Name: "QueueName", Value: { "Fn::GetAtt": ["InboxRealtimeDeadLetterQueue", "QueueName"] } }] },
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
  },
};

module.exports = serverlessConfiguration;
