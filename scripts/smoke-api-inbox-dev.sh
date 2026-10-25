#!/usr/bin/env bash
set -euo pipefail
set +x
umask 077

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Error: falta el comando requerido '$1'." >&2
    exit 1
  fi
}

require_env() {
  if [[ -z "${!1:-}" ]]; then
    echo "Error: falta la variable $1." >&2
    exit 1
  fi
}

require_command curl
require_command node
require_env INBOX_SMOKE_BASE_URL
require_env INBOX_SMOKE_ACCESS_TOKEN
require_env INBOX_SMOKE_VERIFY_TOKEN
require_env INBOX_SMOKE_APP_SECRET

if [[ "${INBOX_SMOKE_STAGE:-}" != "dev" || "${INBOX_SMOKE_CONFIRM:-}" != "DEV_SAFE_SMOKE" ]]; then
  echo "Error: define INBOX_SMOKE_STAGE=dev e INBOX_SMOKE_CONFIRM=DEV_SAFE_SMOKE." >&2
  exit 1
fi

base_url=${INBOX_SMOKE_BASE_URL%/}
if [[ "$base_url" != https://* ]]; then
  echo "Error: INBOX_SMOKE_BASE_URL debe usar HTTPS." >&2
  exit 1
fi

response_file=$(mktemp "${TMPDIR:-/tmp}/api-inbox-smoke.XXXXXX")
trap 'rm -f "$response_file"' EXIT

expect_status() {
  local label=$1
  local expected=$2
  shift 2
  local actual
  actual=$(curl --silent --show-error --connect-timeout 10 --max-time 30 --output "$response_file" --write-out '%{http_code}' "$@")
  if [[ "$actual" != "$expected" ]]; then
    echo "FAIL: $label devolvió HTTP $actual; se esperaba $expected." >&2
    return 1
  fi
  echo "OK: $label ($actual)"
}

challenge="inbox-smoke-$(date -u +%Y%m%dT%H%M%SZ)"
expect_status "verificación con token inválido" 403 \
  --get "$base_url/webhooks/whatsapp" \
  --data-urlencode 'hub.mode=subscribe' \
  --data-urlencode 'hub.verify_token=invalid-smoke-token' \
  --data-urlencode "hub.challenge=$challenge"

expect_status "verificación con token válido" 200 \
  --get "$base_url/webhooks/whatsapp" \
  --data-urlencode 'hub.mode=subscribe' \
  --data-urlencode "hub.verify_token=$INBOX_SMOKE_VERIFY_TOKEN" \
  --data-urlencode "hub.challenge=$challenge"
if [[ "$(<"$response_file")" != "$challenge" ]]; then
  echo "FAIL: el webhook no devolvió el challenge esperado." >&2
  exit 1
fi

safe_payload='{"object":"whatsapp_business_account","entry":[]}'
expect_status "webhook con firma inválida" 401 \
  --request POST "$base_url/webhooks/whatsapp" \
  --header 'content-type: application/json' \
  --header 'x-hub-signature-256: sha256=0000000000000000000000000000000000000000000000000000000000000000' \
  --data-binary "$safe_payload"

signature=$(INBOX_SMOKE_PAYLOAD="$safe_payload" node -e '
  const crypto = require("node:crypto");
  process.stdout.write("sha256=" + crypto.createHmac("sha256", process.env.INBOX_SMOKE_APP_SECRET).update(process.env.INBOX_SMOKE_PAYLOAD).digest("hex"));
')
expect_status "webhook vacío con firma válida" 200 \
  --request POST "$base_url/webhooks/whatsapp" \
  --header 'content-type: application/json' \
  --header "x-hub-signature-256: $signature" \
  --data-binary "$safe_payload"

expect_status "listado autenticado" 200 \
  "$base_url/conversaciones?page=1&pageSize=1" \
  --header "authorization: Bearer $INBOX_SMOKE_ACCESS_TOKEN"

echo "Smoke seguro DEV aprobado. No se enviaron mensajes ni se modificaron conversaciones."
