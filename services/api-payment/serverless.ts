import { ApiServices } from "../../common/api-services.js";
import { REGION, STAGE } from "../../common/custom-parameters.js";
import { buildGoServiceServerless, type GoHttpEndpoint } from "../../common/go-service.js";
import { ssmReadPolicy } from "../../aws/policies/ssm.js";
import type { IamStatement } from "../../aws/policies/types.js";

if (STAGE !== "dev") throw new Error("api-payment está habilitado exclusivamente para DEV");
const tableArn = "${cf:indomito-hub-infra-ddb-dev.PagosTableArn}";
const tableName = "${cf:indomito-hub-infra-ddb-dev.PagosTableName}";
const baseUrl = "${cf:indomito-hub-infra-api-gateway-dev.HttpApiUrl}";
const secrets = ["api-key", "webhook-secret", "receiver-id"].map((name) =>
  ssmReadPolicy({"Fn::Sub": `arn:\${AWS::Partition}:ssm:${REGION}:\${AWS::AccountId}:parameter/indomito/dev/payments/khipu/${name}`}),
);
const read: IamStatement = { Effect:"Allow", Action:["dynamodb:GetItem"], Resource:tableArn };
const write: IamStatement = { Effect:"Allow", Action:["dynamodb:GetItem","dynamodb:PutItem","dynamodb:UpdateItem"], Resource:tableArn };
const endpoints: GoHttpEndpoint[] = [
  {name:"fn-configuracion-pagos-v1",method:"GET",path:"/pagos/configuracion",public:false,timeout:29, policies:secrets},
  {name:"fn-crear-prueba-pago-v1",method:"POST",path:"/pagos/pruebas",public:false,timeout:29, policies:[write, ...secrets]},
  {name:"fn-obtener-prueba-pago-v1",method:"GET",path:"/pagos/pruebas/{id}",public:false,timeout:29, policies:[read, ...secrets]},
  {name:"fn-verificar-prueba-pago-v1",method:"POST",path:"/pagos/pruebas/{id}/verificar",public:false,timeout:29, policies:[write, ...secrets]},
  {name:"fn-notificacion-khipu-v1",method:"POST",path:"/pagos/khipu/notificaciones",public:true,timeout:29, policies:[write, ...secrets]},
  {name:"fn-retorno-pago-v1",method:"GET",path:"/pagos/retorno",public:true,timeout:29, policies:[]},
];
module.exports = buildGoServiceServerless(ApiServices.Payment, endpoints, {
  env: { PAYMENTS_TABLE_NAME: tableName, PAYMENTS_BASE_URL: baseUrl },
});
