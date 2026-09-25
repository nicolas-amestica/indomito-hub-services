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

put_item "{\"pk\":{\"S\":\"APP#INDOMITO_HUB\"},\"sk\":{\"S\":\"APP#INDOMITO_HUB#LV1#PRG#LV2#CONTRACT_CREATE\"},\"code\":{\"S\":\"CONTRACT_CREATE\"},\"title\":{\"S\":\"Crear contrato\"},\"category\":{\"S\":\"Programas\"},\"path\":{\"S\":\"/contratos/nuevo\"},\"icon\":{\"S\":\"icon-[tabler--file-plus]\"},\"order\":{\"N\":\"2\"},\"active\":{\"BOOL\":true},\"level\":{\"S\":\"LV2\"},\"parentCode\":{\"S\":\"PRG\"},\"endpoints\":{\"L\":[{\"S\":\"/contratos\"},{\"S\":\"/contratos:pdf\"},{\"S\":\"/contratos/{id-contrato}/pdf\"},{\"S\":\"/contratos:configuracion\"}]},\"createdAt\":{\"S\":\"$NOW\"}}"

put_item "{\"pk\":{\"S\":\"APP#INDOMITO_HUB\"},\"sk\":{\"S\":\"APP#INDOMITO_HUB#LV1#PRG#LV2#CONTRACT_LIST\"},\"code\":{\"S\":\"CONTRACT_LIST\"},\"title\":{\"S\":\"Listado de contratos\"},\"category\":{\"S\":\"Programas\"},\"path\":{\"S\":\"/contratos\"},\"icon\":{\"S\":\"icon-[tabler--file-description]\"},\"order\":{\"N\":\"3\"},\"active\":{\"BOOL\":true},\"level\":{\"S\":\"LV2\"},\"parentCode\":{\"S\":\"PRG\"},\"endpoints\":{\"L\":[{\"S\":\"/contratos\"},{\"S\":\"/contratos/{id-contrato}/pdf\"}]},\"createdAt\":{\"S\":\"$NOW\"}}"

# El perfil visible "Administrador" usa el código interno ADMIN. ADM es el
# código del módulo padre "Administración", no el código del perfil.
put_item '{"pk":{"S":"APP#PROFILE#PERMISSIONS"},"sk":{"S":"PFL#ADMIN#APP#INDOMITO_HUB#LV1#PRG#LV2#CONTRACT_CREATE"},"moduleCode":{"S":"CONTRACT_CREATE"},"parentCode":{"S":"PRG"},"allowances":{"L":[{"S":"c"},{"S":"r"},{"S":"u"}]}}'
put_item '{"pk":{"S":"APP#PROFILE#PERMISSIONS"},"sk":{"S":"PFL#ADMIN#APP#INDOMITO_HUB#LV1#PRG#LV2#CONTRACT_LIST"},"moduleCode":{"S":"CONTRACT_LIST"},"parentCode":{"S":"PRG"},"allowances":{"L":[{"S":"r"}]}}'

echo "Módulos CONTRACT_CREATE y CONTRACT_LIST configurados en $TABLE para el perfil Administrador (ADMIN)."
