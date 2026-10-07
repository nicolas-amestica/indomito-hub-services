import { ApiServices } from "../../common/api-services.js";
import { REGION, STAGE } from "../../common/custom-parameters.js";
import { buildGoServiceServerless, type GoHttpEndpoint } from "../../common/go-service.js";
import { ssmReadPolicy } from "../../aws/policies/ssm.js";
import type { IamStatement } from "../../aws/policies/types.js";

if (STAGE !== "dev") throw new Error("api-payment está habilitado exclusivamente para DEV");
const tableArn = "${cf:indomito-hub-infra-ddb-dev.PagosTableArn}";
const tableName = "${cf:indomito-hub-infra-ddb-dev.PagosTableName}";
const tableStreamArn = "${cf:indomito-hub-infra-ddb-dev.PagosTableStreamArn}";
const contractsArn = "${cf:indomito-hub-infra-ddb-dev.ProgramasTableArn}";
const contractsName = "${cf:indomito-hub-infra-ddb-dev.ProgramasTableName}";
const baseUrl = "${cf:indomito-hub-infra-api-gateway-dev.HttpApiUrl}";
const secrets = ["api-key", "webhook-secret", "receiver-id"].map((name) =>
  ssmReadPolicy({"Fn::Sub": `arn:\${AWS::Partition}:ssm:${REGION}:\${AWS::AccountId}:parameter/indomito/dev/payments/khipu/${name}`}),
);
const read: IamStatement = { Effect:"Allow", Action:["dynamodb:GetItem"], Resource:tableArn };
const activePlanCheck: IamStatement = {Effect:"Allow",Action:["dynamodb:ConditionCheckItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["TRIP#*"]}}};
const write: IamStatement = { Effect:"Allow", Action:["dynamodb:GetItem","dynamodb:PutItem","dynamodb:UpdateItem"], Resource:tableArn };
const contractRead: IamStatement = { Effect:"Allow", Action:["dynamodb:GetItem"], Resource:contractsArn };
const lookupSecret = ssmReadPolicy({"Fn::Sub": `arn:\${AWS::Partition}:ssm:${REGION}:\${AWS::AccountId}:parameter/indomito/dev/payments/lookup-secret`});
const sessionSecret = ssmReadPolicy({"Fn::Sub": `arn:\${AWS::Partition}:ssm:${REGION}:\${AWS::AccountId}:parameter/indomito/dev/auth/payment-session-secret`});
const lookupRead: IamStatement = {Effect:"Allow",Action:["dynamodb:GetItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["CODE#*","TRIP#*","ACCOUNT#*","RATE#*"]}}};
const rateWrite: IamStatement = {Effect:"Allow",Action:["dynamodb:PutItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["RATE#*"]}}};
const annexRead:IamStatement={Effect:'Allow',Action:['dynamodb:GetItem','dynamodb:Query'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['TRIP#*','ACCOUNT#*']}}};
const annexWrite:IamStatement={Effect:'Allow',Action:['dynamodb:PutItem','dynamodb:ConditionCheckItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['TRIP#*','ACCOUNT#*']}}};
const receiptRead:IamStatement={Effect:'Allow',Action:['dynamodb:GetItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['CODE#*','TRIP#*','ACCOUNT#*','RECEIPT#*']}}};
const receiptQuery:IamStatement={Effect:'Allow',Action:['dynamodb:Query'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['ACCOUNT#*']}}};
const groupReceiptQuery:IamStatement={Effect:'Allow',Action:['dynamodb:Query'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['TRIP#*']}}};
const receiptDownload:IamStatement={Effect:'Allow',Action:['s3:GetObject'],Resource:'${cf:indomito-hub-infra-s3-dev.ReceiptsBucketArn}/receipts/*'};
const taxDocumentRead:IamStatement={Effect:'Allow',Action:['dynamodb:GetItem','dynamodb:Query'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['TAX_REQUEST#*','TAX_QUEUE#PENDING','TAX_QUEUE#RECORDED','COMMAND#*']}}};
const taxDocumentWrite:IamStatement={Effect:'Allow',Action:['dynamodb:PutItem','dynamodb:DeleteItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['TAX_REQUEST#*','TAX_QUEUE#PENDING','TAX_QUEUE#RECORDED','TAX_FOLIO#*','COMMAND#*']}}};
const taxDocumentObjects:IamStatement={Effect:'Allow',Action:['s3:PutObject','s3:GetObject'],Resource:'${cf:indomito-hub-infra-s3-dev.ReceiptsBucketArn}/tax-documents/manual/*'};
const endpoints: GoHttpEndpoint[] = [
	{name:'fn-verificar-comprobante-publico-v1',method:'POST',path:'/pagos/comprobantes/verificaciones',public:true,timeout:10,policies:[{Effect:'Allow',Action:['dynamodb:GetItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['RECEIPT#*','ATTEMPT#*','RATE#*']}}},rateWrite,lookupSecret,sessionSecret]},
	{name:'fn-listar-solicitudes-tributarias-v1',method:'GET',path:'/pagos/documentos-tributarios/pendientes',public:false,timeout:10,policies:[taxDocumentRead]},
	{name:'fn-registrar-boleta-manual-v1',method:'POST',path:'/pagos/documentos-tributarios/{requestId}/emision-manual',public:false,timeout:29,policies:[taxDocumentRead,taxDocumentWrite,taxDocumentObjects]},
	{name:'fn-descargar-boleta-manual-v1',method:'GET',path:'/pagos/documentos-tributarios/{requestId}/descarga',public:false,timeout:10,policies:[taxDocumentRead,taxDocumentObjects]},
  {name:'fn-crear-anexo-v1',method:'POST',path:'/pagos/giras/{tripId}/anexos',public:false,timeout:29,policies:[annexRead,annexWrite,lookupSecret]},
  {name:'fn-listar-nomina-v1',method:'GET',path:'/pagos/giras/{tripId}/pasajeros',public:false,timeout:10,policies:[annexRead]},
  {name:'fn-migrar-nomina-v1',method:'POST',path:'/pagos/giras/{tripId}/pasajeros/migracion',public:false,timeout:29,policies:[contractRead,annexRead,annexWrite]},
  {name:'fn-cerrar-nomina-v1',method:'POST',path:'/pagos/giras/{tripId}/pasajeros/cierre',public:false,timeout:10,policies:[annexRead,annexWrite]},
  {name:'fn-obtener-anexo-v1',method:'GET',path:'/pagos/giras/{tripId}/anexos/{annexId}',public:false,timeout:10,policies:[annexRead]},
  {name:'fn-previsualizar-anexo-v1',method:'GET',path:'/pagos/giras/{tripId}/anexos/{annexId}/propuestas',public:false,timeout:29,policies:[annexRead]},
  {name:'fn-resumir-impacto-anexo-v1',method:'GET',path:'/pagos/giras/{tripId}/anexos/{annexId}/impacto',public:false,timeout:29,policies:[annexRead]},
  {name:'fn-aplicar-anexo-v1',method:'POST',path:'/pagos/giras/{tripId}/anexos/{annexId}/aplicacion',public:false,timeout:29,policies:[annexRead,annexWrite]},
  {name:'fn-registrar-abono-grupal-v1',method:'POST',path:'/pagos/giras/{tripId}/abonos-grupales',public:false,timeout:29,policies:[annexRead,annexWrite,{Effect:'Allow',Action:['dynamodb:GetItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['REFERENCE#*']} }},{Effect:'Allow',Action:['dynamodb:PutItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['REFERENCE#*','RECEIPT#*','JOB#*','CASH#*']}}}]},
  {name:'fn-obtener-abono-grupal-v1',method:'GET',path:'/pagos/giras/{tripId}/abonos-grupales/{commandId}',public:false,timeout:10,policies:[annexRead]},
  {name:'fn-crear-descuento-grupal-v1',method:'POST',path:'/pagos/giras/{tripId}/descuentos',public:false,timeout:29,policies:[annexRead,annexWrite]},
  {name:'fn-obtener-descuento-grupal-v1',method:'GET',path:'/pagos/giras/{tripId}/descuentos/{discountId}',public:false,timeout:10,policies:[annexRead]},
  {name:'fn-aprobar-descuento-grupal-v1',method:'POST',path:'/pagos/giras/{tripId}/descuentos/{discountId}/aprobacion',public:false,timeout:29,policies:[annexRead,annexWrite]},
  {name:'fn-listar-comprobantes-gira-v1',method:'GET',path:'/pagos/giras/{tripId}/comprobantes',public:false,policies:[receiptRead,groupReceiptQuery]},
  {name:'fn-descargar-comprobante-gira-v1',method:'GET',path:'/pagos/giras/{tripId}/comprobantes/{id}/descarga',public:false,policies:[receiptRead,receiptDownload]},
  {name:'fn-reenviar-comprobante-portal-v1',method:'POST',path:'/pagos/portal/intentos/{id}/comprobante/reenvios',public:false,policies:[{Effect:'Allow',Action:['dynamodb:GetItem','dynamodb:PutItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['CODE#*','TRIP#*','ACCOUNT#*','ATTEMPT#*','RECEIPT#*','JOB#*']}}}]},
  {name:'fn-reenviar-comprobante-cuenta-v1',method:'POST',path:'/pagos/cuentas/{accountId}/comprobantes/{id}/reenvios',public:false,policies:[receiptRead,{Effect:'Allow',Action:['dynamodb:PutItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['RECEIPT#*','JOB#*']}}}]},
  {name:'fn-listar-comprobantes-cuenta-v1',method:'GET',path:'/pagos/cuentas/{accountId}/comprobantes',public:false,policies:[receiptRead,receiptQuery]},
  {name:'fn-descargar-comprobante-cuenta-v1',method:'GET',path:'/pagos/cuentas/{accountId}/comprobantes/{id}/descarga',public:false,policies:[receiptRead,receiptDownload]},
  {name:'fn-listar-fallos-comprobantes-v1',method:'GET',path:'/pagos/comprobantes/fallos',public:false,timeout:10,policies:[{Effect:'Allow',Action:['dynamodb:GetItem','dynamodb:Query'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['JOB#*','RECEIPT#*']}}}]},
  {name:'fn-reintentar-comprobante-v1',method:'POST',path:'/pagos/comprobantes/fallos/{receiptId}/reintentos',public:false,timeout:10,policies:[{Effect:'Allow',Action:['dynamodb:GetItem','dynamodb:PutItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['JOB#*','RECEIPT#*','COMMAND#*']}}}]},
  {name:'fn-migrar-comprobantes-v1',method:'POST',path:'/pagos/cuentas/{accountId}/comprobantes/migracion',public:false,timeout:20,policies:[{Effect:'Allow',Action:['dynamodb:GetItem','dynamodb:Query','dynamodb:PutItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['ACCOUNT#*','TRIP#*','RECEIPT#*','JOB#*','COMMAND#*']}}}]},
  {name:"fn-obtener-cuenta-cobranza-v1",method:"GET",path:"/pagos/cuentas/{id}",public:false,timeout:10,policies:[
    {Effect:"Allow",Action:["dynamodb:GetItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["TRIP#*","ACCOUNT#*"]}}},
  ]},
  {name:'fn-listar-intentos-cuenta-v1',method:'GET',path:'/pagos/cuentas/{accountId}/intentos',public:false,timeout:10,policies:[annexRead]},
  {name:'fn-listar-liquidaciones-v1',method:'GET',path:'/pagos/tesoreria/giras/{tripId}/liquidaciones',public:false,timeout:10,policies:[annexRead]},
  {name:'fn-conciliar-liquidacion-v1',method:'POST',path:'/pagos/tesoreria/giras/{tripId}/liquidaciones/{paymentId}',public:false,timeout:15,policies:[annexRead,annexWrite,{Effect:'Allow',Action:['dynamodb:GetItem','dynamodb:PutItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['COMMAND#*','REFERENCE#*','CASH#*']}}}]},
  {name:'fn-obtener-caja-gira-v1',method:'GET',path:'/pagos/tesoreria/giras/{tripId}/caja',public:false,timeout:15,policies:[annexRead]},
  {name:'fn-obtener-caja-consolidada-v1',method:'GET',path:'/pagos/tesoreria/caja',public:false,timeout:20,policies:[{Effect:'Allow',Action:['dynamodb:Query'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['CASH#*']}}}]},
  {name:'fn-listar-proveedores-v1',method:'GET',path:'/pagos/tesoreria/giras/{tripId}/proveedores',public:false,timeout:10,policies:[annexRead]},
  {name:'fn-registrar-operacion-proveedor-v1',method:'POST',path:'/pagos/tesoreria/giras/{tripId}/proveedores/{supplierId}',public:false,timeout:15,policies:[annexRead,annexWrite,{Effect:'Allow',Action:['dynamodb:GetItem','dynamodb:PutItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['COMMAND#*','REFERENCE#*','CASH#*']}}}]},
  {name:'fn-listar-devoluciones-gira-v1',method:'GET',path:'/pagos/tesoreria/giras/{tripId}/devoluciones',public:false,timeout:15,policies:[annexRead]},
  {name:'fn-obtener-alertas-cobranza-v1',method:'GET',path:'/pagos/tesoreria/giras/{tripId}/alertas',public:false,timeout:15,policies:[annexRead]},
  {name:'fn-listar-alertas-cobranza-v1',method:'GET',path:'/pagos/tesoreria/alertas',public:false,timeout:29,policies:[annexRead,{Effect:'Allow',Action:['dynamodb:Query'],Resource:`${contractsArn}/index/gsi-periodo-documental-index`}]},
  {name:'fn-obtener-operacion-financiera-v1',method:'GET',path:'/pagos/operaciones/{id}',public:false,timeout:10,policies:[{Effect:'Allow',Action:['dynamodb:GetItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['COMMAND#*','TRIP#*']}}}]},
  {name:'fn-obtener-acceso-gira-v1',method:'GET',path:'/pagos/giras/{tripId}/acceso',public:false,timeout:10,policies:[{Effect:'Allow',Action:['dynamodb:GetItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['TRIP#*']}}},contractRead]},
  ...['rotate','revoke'].map((action):GoHttpEndpoint=>({name:`fn-cambiar-acceso-gira-v1-${action}`,method:'POST',path:`/pagos/giras/{tripId}/acceso/${action}`,public:false,timeout:10,policies:[{Effect:'Allow',Action:['dynamodb:GetItem','dynamodb:PutItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['TRIP#*','CODE#*','COMMAND#*']}}},lookupSecret]})),
  {name:"fn-registrar-operacion-cuenta-v1",method:"POST",path:"/pagos/cuentas/{id}/operaciones",public:false,timeout:15,policies:[
    activePlanCheck,
    {Effect:"Allow",Action:["dynamodb:GetItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["TRIP#*","ACCOUNT#*","COMMAND#*","REFERENCE#*"]}}},
    {Effect:"Allow",Action:["dynamodb:PutItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["ACCOUNT#*","TRIP#*","COMMAND#*","REFERENCE#*","RECEIPT#*","JOB#*","CASH#*"]}}},
  ]},
  {name:"fn-crear-checkout-cuota-v1",method:"POST",path:"/pagos/portal/checkout",public:false,timeout:29,policies:[
    activePlanCheck,
    {Effect:"Allow",Action:["dynamodb:GetItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["CODE#*","TRIP#*","ACCOUNT#*","ATTEMPT#*","COMMAND#*"]}}},
    {Effect:"Allow",Action:["dynamodb:PutItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["ACCOUNT#*","ATTEMPT#*","COMMAND#*"]}}},...secrets,
  ]},
  {name:"fn-obtener-intento-cuota-v1",method:"GET",path:"/pagos/portal/intentos/{id}",public:false,timeout:10,policies:[
    {Effect:"Allow",Action:["dynamodb:GetItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["CODE#*","TRIP#*","ACCOUNT#*","ATTEMPT#*","COMMAND#*"]}}},
  ]},
  {name:"fn-obtener-cuenta-portal-v1",method:"GET",path:"/pagos/portal/cuenta",public:false,timeout:10,policies:[
    {Effect:"Allow",Action:["dynamodb:GetItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["CODE#*","TRIP#*","ACCOUNT#*"]}}},
  ]},
  {name:'fn-conciliar-intento-khipu-v1',method:'POST',path:'/pagos/intentos/{attemptId}/conciliacion',public:false,timeout:29,policies:[activePlanCheck,{Effect:'Allow',Action:['dynamodb:GetItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['ATTEMPT#*','ACCOUNT#*','TRIP#*','COMMAND#*','REFERENCE#*']}}},{Effect:'Allow',Action:['dynamodb:PutItem'],Resource:tableArn,Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['ATTEMPT#*','ACCOUNT#*','TRIP#*','COMMAND#*','REFERENCE#*','RECEIPT#*','JOB#*','CASH#*','TAX_REQUEST#*','TAX_QUEUE#PENDING']}}},...secrets]},
  {name:"fn-notificacion-cuota-khipu-v1",method:"POST",path:"/pagos/cuotas/khipu/notificaciones",public:true,timeout:29, policies:[
    {Effect:"Allow",Action:["dynamodb:GetItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["ATTEMPT#*","ACCOUNT#*","TRIP#*","COMMAND#*","REFERENCE#*"]}}},
    {Effect:"Allow",Action:["dynamodb:PutItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["PROVIDER_NOTIFICATION#*","PROVIDER_JOB#*"]}}},
    ...secrets,
  ]},
  {name:"fn-procesar-notificacion-cuota-khipu-v1",timeout:29,memorySize:128,events:[{stream:{type:'dynamodb',arn:tableStreamArn,startingPosition:'LATEST',batchSize:1,maximumRetryAttempts:5,maximumRecordAgeInSeconds:3600,bisectBatchOnFunctionError:true,functionResponseType:'ReportBatchItemFailures',filterPatterns:[{eventName:['INSERT','MODIFY'],dynamodb:{NewImage:{pk:{S:[{prefix:'PROVIDER_JOB#'}]},status:{S:['PENDING']}}}}]}}],policies:[
    activePlanCheck,
    {Effect:"Allow",Action:["dynamodb:GetItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["PROVIDER_NOTIFICATION#*","PROVIDER_JOB#*","ATTEMPT#*","ACCOUNT#*","TRIP#*","COMMAND#*","REFERENCE#*"]}}},
    {Effect:"Allow",Action:["dynamodb:PutItem"],Resource:tableArn,Condition:{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["PROVIDER_NOTIFICATION#*","PROVIDER_JOB#*","ACCOUNT#*","TRIP#*","COMMAND#*","REFERENCE#*","RECEIPT#*","JOB#*","ATTEMPT#*","CASH#*","TAX_REQUEST#*","TAX_QUEUE#PENDING"]}}},
    {Effect:"Allow",Action:["dynamodb:DescribeStream","dynamodb:GetRecords","dynamodb:GetShardIterator","dynamodb:ListStreams"],Resource:tableStreamArn},
    ...secrets,
  ]},
  {name:"fn-consultar-cuotas-v1",method:"POST",path:"/pagos/consultas",public:true,timeout:10, policies:[lookupRead,rateWrite,lookupSecret,sessionSecret]},
  {name:"fn-obtener-puesta-marcha-v1",method:"GET",path:"/pagos/contratos/{id}/puesta-en-marcha",public:false,timeout:29, policies:[contractRead,read,lookupSecret]},
  {name:"fn-confirmar-puesta-marcha-v1",method:"POST",path:"/pagos/contratos/{id}/puesta-en-marcha",public:false,timeout:29, policies:[contractRead,write,lookupSecret]},
  {name:"fn-configuracion-pagos-v1",method:"GET",path:"/pagos/configuracion",public:false,timeout:29, policies:secrets},
  {name:"fn-crear-prueba-pago-v1",method:"POST",path:"/pagos/pruebas",public:false,timeout:29, policies:[write, ...secrets]},
  {name:"fn-obtener-prueba-pago-v1",method:"GET",path:"/pagos/pruebas/{id}",public:false,timeout:29, policies:[read, ...secrets]},
  {name:"fn-verificar-prueba-pago-v1",method:"POST",path:"/pagos/pruebas/{id}/verificar",public:false,timeout:29, policies:[write, ...secrets]},
  {name:"fn-notificacion-khipu-v1",method:"POST",path:"/pagos/khipu/notificaciones",public:true,timeout:29, policies:[write, ...secrets]},
  {name:"fn-retorno-pago-v1",method:"GET",path:"/pagos/retorno",public:true,timeout:29, policies:[]},
];
module.exports = buildGoServiceServerless(ApiServices.Payment, endpoints, {
  env: { PAYMENTS_TABLE_NAME: tableName, PAYMENTS_BASE_URL: baseUrl, PAYMENTS_PORTAL_URL:'https://pagos.dev.girasindomito.cl', PAYMENTS_CHECKOUT_ENABLED:'true', PROGRAMS_TABLE_NAME:contractsName, RECEIPTS_BUCKET_NAME:'${cf:indomito-hub-infra-s3-dev.ReceiptsBucketName}' },
});
