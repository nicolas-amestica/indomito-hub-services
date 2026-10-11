#!/usr/bin/env bash
set -euo pipefail
PROFILE="${AWS_PROFILE:-pa-dev}"
REGION="${AWS_REGION:-us-east-1}"
STACK="indomito-hub-infra-ddb-dev"
TABLE="$(aws cloudformation describe-stacks --stack-name "$STACK" --profile "$PROFILE" --region "$REGION" --query "Stacks[0].Outputs[?OutputKey=='UsuariosTableName'].OutputValue | [0]" --output text --no-cli-pager)"
[[ -n "$TABLE" && "$TABLE" != "None" ]] || { echo "No se pudo resolver UsuariosTableName" >&2; exit 1; }
NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
aws dynamodb put-item --table-name "$TABLE" --profile "$PROFILE" --region "$REGION" --no-cli-pager --item "{\"pk\":{\"S\":\"APP#INDOMITO_HUB\"},\"sk\":{\"S\":\"APP#INDOMITO_HUB#LV1#ADM#LV2#MEET_AGENDA\"},\"code\":{\"S\":\"MEET_AGENDA\"},\"title\":{\"S\":\"Agenda de Videollamadas\"},\"category\":{\"S\":\"Administración\"},\"path\":{\"S\":\"/agenda/videollamadas\"},\"icon\":{\"S\":\"icon-[tabler--video]\"},\"order\":{\"N\":\"6\"},\"active\":{\"BOOL\":true},\"level\":{\"S\":\"LV2\"},\"parentCode\":{\"S\":\"ADM\"},\"endpoints\":{\"L\":[{\"S\":\"/v1/reuniones\"},{\"S\":\"/v1/reuniones/{eventId}\"}]},\"createdAt\":{\"S\":\"$NOW\"}}"
aws dynamodb put-item --table-name "$TABLE" --profile "$PROFILE" --region "$REGION" --no-cli-pager --item '{"pk":{"S":"APP#PROFILE#PERMISSIONS"},"sk":{"S":"PFL#ADMIN#APP#INDOMITO_HUB#LV1#ADM#LV2#MEET_AGENDA"},"moduleCode":{"S":"MEET_AGENDA"},"parentCode":{"S":"ADM"},"allowances":{"L":[{"S":"c"},{"S":"r"},{"S":"u"},{"S":"d"}]}}'
echo "MEET_AGENDA registrado para ADMIN. Requiere refresh de permisos."
