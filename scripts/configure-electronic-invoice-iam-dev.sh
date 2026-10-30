#!/usr/bin/env bash
set -euo pipefail

STAGE=${1:-dev}
PROFILE=${2:-pa-dev}
REGION=${3:-us-east-1}
STACK="indomito-hub-infra-ddb-${STAGE}"
TABLE=${IAM_TABLE_NAME:-$(aws cloudformation describe-stacks --stack-name "$STACK" --profile "$PROFILE" --region "$REGION" --query "Stacks[0].Outputs[?OutputKey=='UsuariosTableName'].OutputValue | [0]" --output text --no-cli-pager)}
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
MODULE_SK="APP#INDOMITO_HUB#LV1#PAY#LV2#FACTURA_ELECTRONICA"
PERMISSION_SK="PFL#ADMIN#APP#INDOMITO_HUB#LV1#PAY#LV2#FACTURA_ELECTRONICA"
aws_args=(--profile "$PROFILE" --region "$REGION" --no-cli-pager)

if [[ -z "$TABLE" || "$TABLE" == "None" ]]; then
  echo "Error: no se pudo resolver UsuariosTableName desde $STACK." >&2
  exit 1
fi

parent=$(aws dynamodb get-item "${aws_args[@]}" --table-name "$TABLE" --consistent-read --key '{"pk":{"S":"APP#INDOMITO_HUB"},"sk":{"S":"APP#INDOMITO_HUB#LV1#PAY"}}' --output json)
profile=$(aws dynamodb get-item "${aws_args[@]}" --table-name "$TABLE" --consistent-read --key '{"pk":{"S":"PROFILES"},"sk":{"S":"PROFILE#ADMIN"}}' --output json)
if [[ $(jq -r '.Item.active.BOOL // false' <<<"$parent") != true || $(jq -r '.Item.title.S // ""' <<<"$parent") != "Pagos y cobranza" ]]; then
  echo "Error: el módulo LV1 PAY no existe o no corresponde a Pagos y cobranza." >&2
  exit 1
fi
if [[ $(jq -r '.Item.active.BOOL // false' <<<"$profile") != true ]]; then
  echo "Error: el perfil administrador persistido como ADMIN no existe o está inactivo." >&2
  exit 1
fi

existing=$(aws dynamodb get-item "${aws_args[@]}" --table-name "$TABLE" --consistent-read --key "{\"pk\":{\"S\":\"APP#INDOMITO_HUB\"},\"sk\":{\"S\":\"$MODULE_SK\"}}" --output json)
CREATED_AT=$(jq -r --arg now "$NOW" '.Item.createdAt.S // $now' <<<"$existing")

aws dynamodb put-item "${aws_args[@]}" --table-name "$TABLE" --item "{\"pk\":{\"S\":\"APP#INDOMITO_HUB\"},\"sk\":{\"S\":\"$MODULE_SK\"},\"code\":{\"S\":\"FACTURA_ELECTRONICA\"},\"title\":{\"S\":\"Factura electrónica\"},\"category\":{\"S\":\"Pagos y cobranza\"},\"path\":{\"S\":\"/cobranza/factura-electronica\"},\"icon\":{\"S\":\"icon-[tabler--receipt-tax]\"},\"order\":{\"N\":\"3\"},\"active\":{\"BOOL\":true},\"level\":{\"S\":\"LV2\"},\"parentCode\":{\"S\":\"PAY\"},\"endpoints\":{\"L\":[{\"S\":\"/pagos/documentos-tributarios\"}]},\"tooltip\":{\"S\":\"Emitir y gestionar boletas electrónicas\"},\"color\":{\"S\":\"lime\"},\"isDeprecated\":{\"BOOL\":false},\"createdAt\":{\"S\":\"$CREATED_AT\"}}"
aws dynamodb put-item "${aws_args[@]}" --table-name "$TABLE" --item "{\"pk\":{\"S\":\"APP#PROFILE#PERMISSIONS\"},\"sk\":{\"S\":\"$PERMISSION_SK\"},\"moduleCode\":{\"S\":\"FACTURA_ELECTRONICA\"},\"parentCode\":{\"S\":\"PAY\"},\"allowances\":{\"L\":[{\"S\":\"c\"},{\"S\":\"r\"},{\"S\":\"u\"}]}}"

echo "FACTURA_ELECTRONICA registrado bajo PAY y asignado al perfil ADMIN (Administrador)."
