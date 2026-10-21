import { dynamodbCrudPolicy } from "../../aws/policies/dynamodb.js";
import { ssmReadPolicy } from "../../aws/policies/ssm.js";
import { s3ObjectPolicy, s3ReadPolicy } from "../../aws/policies/s3.js";
import { ApiServices } from "../../common/api-services.js";
import { REGION, STAGE } from "../../common/custom-parameters.js";
import {
  buildGoServiceServerless,
  type GoHttpEndpoint,
} from "../../common/go-service.js";
const DDB_STACK = `indomito-hub-infra-ddb-${STAGE}`;
const TABLE_ARN = `\${cf:${DDB_STACK}.UsuariosTableArn}`;
const TABLE_NAME = `\${cf:${DDB_STACK}.UsuariosTableName}`;
const S3_STACK = `indomito-hub-infra-s3-${STAGE}`;
const PROFILE_PHOTOS_BUCKET_ARN = `\${cf:${S3_STACK}.MediaBucketArn}`;
const PROFILE_PHOTOS_BUCKET_NAME = `\${cf:${S3_STACK}.MediaBucketName}`;
const PARAM = `/indomito/${STAGE}/auth/jwt-secret`;
const PASSWORD_RESET_EMAIL_PARAM = `/indomito/${STAGE}/auth/password-reset-email`;
const PARAM_ARN = {
  "Fn::Sub": `arn:\${AWS::Partition}:ssm:${REGION}:\${AWS::AccountId}:parameter${PARAM}`,
};
const PASSWORD_RESET_EMAIL_PARAM_ARN = {
  "Fn::Sub": `arn:\${AWS::Partition}:ssm:${REGION}:\${AWS::AccountId}:parameter${PASSWORD_RESET_EMAIL_PARAM}`,
};
const rw = [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(PARAM_ARN)];
const profilePhotoPolicies = [s3ObjectPolicy(`${PROFILE_PHOTOS_BUCKET_ARN}/profile-photos/*`)];
const sessionPolicies = [...rw, s3ReadPolicy(`${PROFILE_PHOTOS_BUCKET_ARN}/profile-photos/*`)];
const passwordResetPolicies = [dynamodbCrudPolicy(TABLE_ARN), ssmReadPolicy(PASSWORD_RESET_EMAIL_PARAM_ARN)];
const endpoints: GoHttpEndpoint[] = [
  {
    name: "fn-login-v1",
    method: "POST",
    path: "/auth/login",
    public: true,
    timeout: 10,
    policies: sessionPolicies,
  },
  {
    name: "fn-solicitar-recuperacion-clave-v1",
    method: "POST",
    path: "/auth/recuperar-clave",
    public: true,
    timeout: 25,
    policies: passwordResetPolicies,
  },
  {
    name: "fn-restablecer-clave-v1",
    method: "POST",
    path: "/auth/restablecer-clave",
    public: true,
    timeout: 10,
    policies: passwordResetPolicies,
  },
  {
    name: "fn-obtener-permisos-v1",
    method: "GET",
    path: "/auth/permisos",
    timeout: 10,
    policies: sessionPolicies,
  },
  { name: "fn-obtener-foto-perfil-v1", method: "GET", path: "/auth/foto-perfil", policies: profilePhotoPolicies },
  { name: "fn-guardar-foto-perfil-v1", method: "PUT", path: "/auth/foto-perfil", policies: profilePhotoPolicies },
  { name: "fn-eliminar-foto-perfil-v1", method: "DELETE", path: "/auth/foto-perfil", policies: profilePhotoPolicies },
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
  env: { USERS_TABLE_NAME: TABLE_NAME, JWT_SIGNING_SECRET_PARAM: PARAM, PASSWORD_RESET_EMAIL_PARAM, PROFILE_PHOTOS_BUCKET_NAME },
});
