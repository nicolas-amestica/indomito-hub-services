import { dynamodbCrudPolicy } from "../../aws/policies/dynamodb.js";
import { ssmReadPolicy } from "../../aws/policies/ssm.js";
import { ApiServices } from "../../common/api-services.js";
import { REGION, STAGE } from "../../common/custom-parameters.js";
import {
  buildGoServiceServerless,
  type GoHttpEndpoint,
} from "../../common/go-service.js";
const DDB_STACK = `indomito-hub-infra-ddb-${STAGE}`;
const TABLE_ARN = `\${cf:${DDB_STACK}.UsuariosTableArn}`;
const TABLE_NAME = `\${cf:${DDB_STACK}.UsuariosTableName}`;
const PARAM = `/indomito/${STAGE}/auth/jwt-secret`;
const PARAM_ARN = {
  "Fn::Sub": `arn:\${AWS::Partition}:ssm:${REGION}:\${AWS::AccountId}:parameter${PARAM}`,
};
const rw = [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(PARAM_ARN)];
const endpoints: GoHttpEndpoint[] = [
  {
    name: "fn-login-v1",
    method: "POST",
    path: "/auth/login",
    public: true,
    policies: rw,
  },
  {
    name: "fn-obtener-permisos-v1",
    method: "GET",
    path: "/auth/permisos",
    policies: rw,
  },
  {
    name: "fn-listar-usuarios-v1",
    method: "GET",
    path: "/iam/usuarios",
    policies: rw,
  },
  {
    name: "fn-guardar-usuario-v1",
    method: "POST",
    path: "/iam/usuarios",
    policies: rw,
  },
  {
    name: "fn-listar-perfiles-v1",
    method: "GET",
    path: "/iam/perfiles",
    policies: rw,
  },
  {
    name: "fn-guardar-perfil-v1",
    method: "POST",
    path: "/iam/perfiles",
    policies: rw,
  },
  {
    name: "fn-obtener-permisos-perfil-v1",
    method: "GET",
    path: "/iam/perfiles/{codigo}/permisos",
    policies: rw,
  },
  {
    name: "fn-listar-modulos-v1",
    method: "GET",
    path: "/iam/modulos",
    policies: rw,
  },
  {
    name: "fn-guardar-modulo-v1",
    method: "POST",
    path: "/iam/modulos",
    policies: rw,
  },
  {
    name: "fn-guardar-permisos-v1",
    method: "PUT",
    path: "/iam/perfiles/{codigo}/permisos",
    policies: rw,
  },
];
module.exports = buildGoServiceServerless(ApiServices.Identity, endpoints, {
  env: { USERS_TABLE_NAME: TABLE_NAME, JWT_SIGNING_SECRET_PARAM: PARAM },
});
