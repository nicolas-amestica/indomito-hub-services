#!/usr/bin/env bash
set -euo pipefail

PROFILE="pa-dev"
REGION="us-east-1"
STACK="indomito-hub-infra-ddb-dev"
REMOVE_LEGACY_CATALOG="false"

case "${1:-}" in
  "") ;;
  --remove-legacy-catalog) REMOVE_LEGACY_CATALOG="true" ;;
  *) echo "Uso: $0 [--remove-legacy-catalog]" >&2; exit 2 ;;
esac
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ITEM_FILE="$(mktemp)"
trap 'rm -f "$ITEM_FILE"' EXIT
SOURCES=(
  "$ROOT_DIR/services/api-contract/config/contract-form.ctx.json"
  "$ROOT_DIR/services/api-contract/config/program-form.pgr.json"
)

TABLE="$(aws cloudformation describe-stacks --stack-name "$STACK" --profile "$PROFILE" --region "$REGION" --query "Stacks[0].Outputs[?OutputKey=='CatalogosTableName'].OutputValue | [0]" --output text --no-cli-pager)"
if [[ -z "$TABLE" || "$TABLE" == "None" ]]; then
  echo "No se pudo resolver la tabla de catalogos desde $STACK" >&2
  exit 1
fi

for SOURCE in "${SOURCES[@]}"; do
python3 - "$SOURCE" "$ITEM_FILE" <<'PY'
import json
import sys

def attribute(value):
    if isinstance(value, bool):
        return {"BOOL": value}
    if isinstance(value, (int, float)):
        return {"N": str(value)}
    if isinstance(value, str):
        return {"S": value}
    if isinstance(value, list):
        return {"L": [attribute(item) for item in value]}
    if isinstance(value, dict):
        return {"M": {key: attribute(item) for key, item in value.items()}}
    if value is None:
        return {"NULL": True}
    raise TypeError(type(value))

with open(sys.argv[1], encoding="utf-8") as source:
    document = json.load(source)
item = {key: attribute(value) for key, value in document.items()}
with open(sys.argv[2], "w", encoding="utf-8") as target:
    json.dump(item, target, ensure_ascii=False)
PY

aws dynamodb put-item --table-name "$TABLE" --item "file://$ITEM_FILE" --profile "$PROFILE" --region "$REGION" --no-cli-pager
echo "Configuración $(basename "$SOURCE") guardada en $TABLE."
done

if [[ "$REMOVE_LEGACY_CATALOG" == "true" ]]; then
  while true; do
    KEYS="$(aws dynamodb query \
      --table-name "$TABLE" \
      --key-condition-expression 'pk = :pk' \
      --expression-attribute-values '{":pk":{"S":"CATALOG"}}' \
      --projection-expression 'pk, sk' \
      --limit 25 \
      --profile "$PROFILE" \
      --region "$REGION" \
      --output json \
      --no-cli-pager)"
    COUNT="$(python3 -c 'import json,sys; print(len(json.load(sys.stdin)["Items"]))' <<<"$KEYS")"
    if [[ "$COUNT" == "0" ]]; then
      break
    fi
    python3 -c 'import json,sys; table=sys.argv[1]; data=json.load(sys.stdin); print(json.dumps({table:[{"DeleteRequest":{"Key":item}} for item in data["Items"]]}))' "$TABLE" <<<"$KEYS" >"$ITEM_FILE"
    aws dynamodb batch-write-item \
      --request-items "file://$ITEM_FILE" \
      --profile "$PROFILE" \
      --region "$REGION" \
      --no-cli-pager
  done
  echo "Partición legacy CATALOG eliminada de $TABLE."
else
  echo "La partición CATALOG se conserva para compatibilidad con el frontend desplegado."
  echo "Después de desplegar el frontend nuevo, ejecuta nuevamente con --remove-legacy-catalog."
fi
