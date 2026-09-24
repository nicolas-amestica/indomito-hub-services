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
    timeout: 10,
    policies: rw,
  },
  {
    name: "fn-obtener-permisos-v1",
    method: "GET",
    path: "/auth/permisos",
    timeout: 10,
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
  { name: "fn-actualizar-usuario-v1", method: "PUT", path: "/iam/usuarios/{id}", policies: rw },
  { name: "fn-cambiar-estado-usuario-v1", method: "PATCH", path: "/iam/usuarios/{id}", policies: rw },
  { name: "fn-eliminar-usuario-v1", method: "DELETE", path: "/iam/usuarios/{id}", policies: rw },
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
  { name: "fn-actualizar-perfil-v1", method: "PUT", path: "/iam/perfiles/{codigo}", policies: rw },
  { name: "fn-eliminar-perfil-v1", method: "DELETE", path: "/iam/perfiles/{codigo}", policies: rw },
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
  { name: "fn-actualizar-modulo-v1", method: "PUT", path: "/iam/modulos/{codigo}", policies: rw },
  { name: "fn-eliminar-modulo-v1", method: "DELETE", path: "/iam/modulos/{codigo}", policies: rw },
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
