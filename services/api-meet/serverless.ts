import { ApiServices } from "../../common/api-services.js";
import { REGION, STAGE } from "../../common/custom-parameters.js";
import { buildGoServiceServerless, type GoHttpEndpoint } from "../../common/go-service.js";
import { ssmReadPolicy } from "../../aws/policies/ssm.js";

if (STAGE !== "dev") throw new Error("api-meet habilitado solo en DEV hasta validar OAuth y autorización");
const parameterArn = {"Fn::Sub": `arn:\${AWS::Partition}:ssm:${REGION}:\${AWS::AccountId}:parameter/indomito/${STAGE}/google/*`};
const endpoints: GoHttpEndpoint[] = [
  {name:"fn-listar-reuniones-v1",method:"GET",path:"/v1/reuniones",public:false,timeout:30,memorySize:128},
  {name:"fn-crear-reunion-v1",method:"POST",path:"/v1/reuniones",public:false,timeout:30,memorySize:128},
  {name:"fn-actualizar-reunion-v1",method:"PUT",path:"/v1/reuniones/{eventId}",public:false,timeout:30,memorySize:128},
  {name:"fn-eliminar-reunion-v1",method:"DELETE",path:"/v1/reuniones/{eventId}",public:false,timeout:30,memorySize:128},
];
module.exports = buildGoServiceServerless(ApiServices.Meet,endpoints,{
  env:{
    GOOGLE_CALENDAR_ID:`\${ssm:/indomito/${STAGE}/google/calendar-id}`,
    GOOGLE_CALENDAR_OAUTH_SSM_PATH:`/indomito/${STAGE}/google/calendar-oauth`,
  },
  policies:[ssmReadPolicy(parameterArn)],
});
