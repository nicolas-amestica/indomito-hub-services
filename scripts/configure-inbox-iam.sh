#!/usr/bin/env bash
set -euo pipefail

STAGE=${1:-dev}
PROFILE=${2:-pa-dev}
REGION=${3:-us-east-1}
STACK="indomito-hub-infra-ddb-${STAGE}"
TABLE=${IAM_TABLE_NAME:-$(aws cloudformation describe-stacks --stack-name "$STACK" --profile "$PROFILE" --region "$REGION" --query "Stacks[0].Outputs[?OutputKey=='UsuariosTableName'].OutputValue | [0]" --output text --no-cli-pager)}
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)

aws_args=(--profile "$PROFILE" --region "$REGION" --no-cli-pager)
get_item() { aws dynamodb get-item "${aws_args[@]}" --table-name "$TABLE" --consistent-read --key "$1" --output json; }

if [[ -z "$TABLE" || "$TABLE" == "None" ]]; then echo "Error: no se pudo resolver UsuariosTableName desde $STACK." >&2; exit 1; fi

admin=$(get_item '{"pk":{"S":"PROFILES"},"sk":{"S":"PROFILE#ADM"}}')
if [[ $(jq -r '.Item.active.BOOL // false' <<<"$admin") != true ]]; then
  echo "Error: el perfil funcional ADM no existe o no está activo." >&2
  exit 1
fi

for order in 50 51; do
  collision=$(aws dynamodb query "${aws_args[@]}" --table-name "$TABLE" --consistent-read \
    --key-condition-expression 'pk = :pk' --filter-expression '#order = :order AND NOT (#code IN (:com,:inbox))' \
    --expression-attribute-names '{"#order":"order","#code":"code"}' \
    --expression-attribute-values "{\":pk\":{\"S\":\"APP#INDOMITO_HUB\"},\":order\":{\"N\":\"$order\"},\":com\":{\"S\":\"COM\"},\":inbox\":{\"S\":\"INBOX\"}}" --select COUNT --query Count --output text)
  if [[ "$collision" != 0 ]]; then echo "Error: order $order ya pertenece a otro módulo." >&2; exit 1; fi
done

created_at() {
  local key=$1 existing
  existing=$(get_item "$key")
  jq -r --arg now "$NOW" '.Item.createdAt.S // $now' <<<"$existing"
}

COM_KEY='{"pk":{"S":"APP#INDOMITO_HUB"},"sk":{"S":"APP#INDOMITO_HUB#LV1#COM"}}'
INBOX_KEY='{"pk":{"S":"APP#INDOMITO_HUB"},"sk":{"S":"APP#INDOMITO_HUB#LV1#COM#LV2#INBOX"}}'
COM_CREATED=$(created_at "$COM_KEY")
INBOX_CREATED=$(created_at "$INBOX_KEY")

aws dynamodb put-item "${aws_args[@]}" --table-name "$TABLE" --item "{\"pk\":{\"S\":\"APP#INDOMITO_HUB\"},\"sk\":{\"S\":\"APP#INDOMITO_HUB#LV1#COM\"},\"code\":{\"S\":\"COM\"},\"title\":{\"S\":\"Comunicaciones\"},\"category\":{\"S\":\"Comunicaciones\"},\"path\":{\"S\":\"/comunicaciones\"},\"icon\":{\"S\":\"icon-[tabler--messages]\"},\"order\":{\"N\":\"50\"},\"active\":{\"BOOL\":true},\"endpoints\":{\"L\":[]},\"tooltip\":{\"S\":\"Gestión de comunicaciones\"},\"color\":{\"S\":\"emerald\"},\"level\":{\"S\":\"LV1\"},\"parentCode\":{\"S\":\"\"},\"isDeprecated\":{\"BOOL\":false},\"createdAt\":{\"S\":\"$COM_CREATED\"}}"
aws dynamodb put-item "${aws_args[@]}" --table-name "$TABLE" --item "{\"pk\":{\"S\":\"APP#INDOMITO_HUB\"},\"sk\":{\"S\":\"APP#INDOMITO_HUB#LV1#COM#LV2#INBOX\"},\"code\":{\"S\":\"INBOX\"},\"title\":{\"S\":\"Conversaciones\"},\"category\":{\"S\":\"Comunicaciones\"},\"path\":{\"S\":\"/comunicaciones/conversaciones\"},\"icon\":{\"S\":\"icon-[tabler--message-circle]\"},\"order\":{\"N\":\"51\"},\"active\":{\"BOOL\":true},\"level\":{\"S\":\"LV2\"},\"parentCode\":{\"S\":\"COM\"},\"endpoints\":{\"L\":[{\"S\":\"/conversaciones\"},{\"S\":\"/envios-inbox\"},{\"S\":\"/operaciones-inbox\"}]},\"tooltip\":{\"S\":\"Gestionar conversaciones de WhatsApp\"},\"color\":{\"S\":\"emerald\"},\"isDeprecated\":{\"BOOL\":false},\"createdAt\":{\"S\":\"$INBOX_CREATED\"}}"
aws dynamodb put-item "${aws_args[@]}" --table-name "$TABLE" --item '{"pk":{"S":"APP#PROFILE#PERMISSIONS"},"sk":{"S":"PFL#ADM#APP#INDOMITO_HUB#LV1#COM"},"moduleCode":{"S":"COM"},"parentCode":{"S":""},"allowances":{"L":[{"S":"r"}]}}'
aws dynamodb put-item "${aws_args[@]}" --table-name "$TABLE" --item '{"pk":{"S":"APP#PROFILE#PERMISSIONS"},"sk":{"S":"PFL#ADM#APP#INDOMITO_HUB#LV1#COM#LV2#INBOX"},"moduleCode":{"S":"INBOX"},"parentCode":{"S":"COM"},"allowances":{"L":[{"S":"c"},{"S":"r"},{"S":"u"},{"S":"d"}]}}'
echo "Inbox registrado idempotentemente para ADM; renueva permisos o inicia sesión nuevamente."
