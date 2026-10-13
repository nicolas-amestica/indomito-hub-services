#!/usr/bin/env bash
set -euo pipefail
PROFILE="pa-dev"
REGION="us-east-1"
STACK="indomito-hub-infra-ddb-dev"
MODULE_SK="APP#INDOMITO_HUB#LV1#ADM#LV2#CONFIGURATION"
ENDPOINT="/configuracion/anfitriones"
TABLE="$(aws cloudformation describe-stacks \
  --stack-name "$STACK" \
  --profile "$PROFILE" \
  --region "$REGION" \
  --query "Stacks[0].Outputs[?OutputKey=='UsuariosTableName'].OutputValue | [0]" \
  --output text \
  --no-cli-pager)"
if [[ -z "$TABLE" || "$TABLE" == "None" ]]; then
  echo "No se pudo resolver la tabla de usuarios desde $STACK" >&2
  exit 1
fi
HAS_ENDPOINT="$(aws dynamodb get-item \
  --table-name "$TABLE" \
  --key "{\"pk\":{\"S\":\"APP#INDOMITO_HUB\"},\"sk\":{\"S\":\"$MODULE_SK\"}}" \
  --projection-expression "endpoints" \
  --profile "$PROFILE" \
  --region "$REGION" \
  --query "length(Item.endpoints.L[?S=='$ENDPOINT'])" \
  --output text \
  --no-cli-pager)"
# El authorizer compara con methodArn.includes(endpoint): una sola entrada cubre
# el listado, las opciones (:opciones) y las operaciones por id de anfitriones.
# Los allowances de ADMIN sobre CONFIGURATION (c,r,u,d) ya existen y no se tocan.
if [[ "$HAS_ENDPOINT" == "0" ]]; then
  aws dynamodb update-item \
    --table-name "$TABLE" \
    --key "{\"pk\":{\"S\":\"APP#INDOMITO_HUB\"},\"sk\":{\"S\":\"$MODULE_SK\"}}" \
    --update-expression "SET endpoints = list_append(if_not_exists(endpoints, :empty), :endpoint)" \
    --condition-expression "attribute_exists(pk) AND attribute_exists(sk)" \
    --expression-attribute-values "{\":empty\":{\"L\":[]},\":endpoint\":{\"L\":[{\"S\":\"$ENDPOINT\"}]}}" \
    --profile "$PROFILE" \
    --region "$REGION" \
    --no-cli-pager
  echo "Anfitriones agregado al módulo CONFIGURATION en $TABLE."
else
  echo "El endpoint $ENDPOINT ya está registrado en CONFIGURATION en $TABLE."
fi
echo "Cierra sesión y vuelve a ingresar para emitir un token con el endpoint actualizado."
