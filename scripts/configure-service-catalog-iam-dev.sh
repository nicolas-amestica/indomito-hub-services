#!/usr/bin/env bash
set -euo pipefail

PROFILE="pa-dev"
REGION="us-east-1"
STACK="indomito-hub-infra-ddb-dev"
TABLE="$(aws cloudformation describe-stacks --stack-name "$STACK" --profile "$PROFILE" --region "$REGION" --query "Stacks[0].Outputs[?OutputKey=='UsuariosTableName'].OutputValue | [0]" --output text --no-cli-pager)"
NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if [[ -z "$TABLE" || "$TABLE" == "None" ]]; then
  echo "No se pudo resolver la tabla de usuarios desde $STACK" >&2
  exit 1
fi

put_item() {
  aws dynamodb put-item --table-name "$TABLE" --item "$1" --profile "$PROFILE" --region "$REGION" --no-cli-pager
}

put_item "{\"pk\":{\"S\":\"APP#INDOMITO_HUB\"},\"sk\":{\"S\":\"APP#INDOMITO_HUB#LV1#ADM#LV2#SERVICE_CATALOG\"},\"code\":{\"S\":\"SERVICE_CATALOG\"},\"title\":{\"S\":\"Catálogo de servicios\"},\"category\":{\"S\":\"Administración\"},\"path\":{\"S\":\"/administracion/catalogo-servicios\"},\"icon\":{\"S\":\"icon-[tabler--list-details]\"},\"order\":{\"N\":\"3\"},\"active\":{\"BOOL\":true},\"level\":{\"S\":\"LV2\"},\"parentCode\":{\"S\":\"ADM\"},\"endpoints\":{\"L\":[{\"S\":\"/catalogo:servicios\"},{\"S\":\"/catalogo:servicios/{id}\"}]},\"createdAt\":{\"S\":\"$NOW\"}}"
put_item '{"pk":{"S":"APP#PROFILE#PERMISSIONS"},"sk":{"S":"PFL#ADMIN#APP#INDOMITO_HUB#LV1#ADM#LV2#SERVICE_CATALOG"},"moduleCode":{"S":"SERVICE_CATALOG"},"parentCode":{"S":"ADM"},"allowances":{"L":[{"S":"c"},{"S":"r"},{"S":"u"}]}}'

# La lectura activa también pertenece al flujo Crear cotización. Se agrega al
# módulo PROGRAMS sin reemplazar su lista vigente ni conceder escrituras.
PROGRAMS_SK="APP#INDOMITO_HUB#LV1#PRG#LV2#PROGRAMS"
HAS_CATALOG_ENDPOINT="$(aws dynamodb get-item \
  --table-name "$TABLE" \
  --key "{\"pk\":{\"S\":\"APP#INDOMITO_HUB\"},\"sk\":{\"S\":\"$PROGRAMS_SK\"}}" \
  --projection-expression "endpoints" \
  --profile "$PROFILE" \
  --region "$REGION" \
  --query "length(Item.endpoints.L[?S=='/catalogo:servicios'])" \
  --output text \
  --no-cli-pager)"

if [[ "$HAS_CATALOG_ENDPOINT" == "0" ]]; then
  aws dynamodb update-item \
    --table-name "$TABLE" \
    --key "{\"pk\":{\"S\":\"APP#INDOMITO_HUB\"},\"sk\":{\"S\":\"$PROGRAMS_SK\"}}" \
    --update-expression "SET endpoints = list_append(if_not_exists(endpoints, :empty), :endpoint)" \
    --condition-expression "attribute_exists(pk) AND attribute_exists(sk)" \
    --expression-attribute-values '{":empty":{"L":[]},":endpoint":{"L":[{"S":"/catalogo:servicios"}]}}' \
    --profile "$PROFILE" \
    --region "$REGION" \
    --no-cli-pager
fi

echo "Módulo SERVICE_CATALOG configurado para ADMIN y lectura agregada a PROGRAMS en $TABLE."
