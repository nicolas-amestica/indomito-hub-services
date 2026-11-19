import { ApiServices } from '../../common/api-services.js';
import { STAGE } from '../../common/custom-parameters.js';
import { buildGoServiceServerless, type GoHttpEndpoint } from '../../common/go-service.js';
import type { IamStatement } from '../../aws/policies/types.js';
const stack = `indomito-hub-infra-ddb-${STAGE}`;
const programArn = `\${cf:${stack}.ProgramasTableArn}`;
const paymentArn = `\${cf:${stack}.PagosTableArn}`;
const read: IamStatement = { Effect:'Allow', Action:['dynamodb:GetItem','dynamodb:Query'], Resource:[programArn,`${programArn}/index/gsi-periodo-documental-index`,paymentArn], Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['TRIP#*','ACCOUNT#*','CONTRACT#*']}} };
// Solo escribe planificación en Programas. No tiene permiso de escritura en Pagos.
const write: IamStatement = { Effect:'Allow', Action:['dynamodb:PutItem'], Resource:programArn, Condition:{'ForAllValues:StringLike':{'dynamodb:LeadingKeys':['TRIP#*']}} };
const endpoints: GoHttpEndpoint[] = [
 {name:'fn-listar-balance-v1',method:'GET',path:'/analitica/giras',public:false,timeout:29,policies:[read]},
 {name:'fn-obtener-balance-v1',method:'GET',path:'/analitica/giras/{id-viaje}',public:false,timeout:29,policies:[read]},
 {name:'fn-guardar-balance-v1',method:'PUT',path:'/analitica/giras/{id-viaje}/presupuesto',public:false,timeout:29,policies:[read,write]},
];
const config = buildGoServiceServerless(ApiServices.Analytics,endpoints,{env:{PROGRAMS_TABLE_NAME:`\${cf:${stack}.ProgramasTableName}`,PAYMENTS_TABLE_NAME:`\${cf:${stack}.PagosTableName}`}});
module.exports = config;
