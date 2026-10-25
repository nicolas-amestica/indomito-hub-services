#!/usr/bin/env bash

set -euo pipefail
set +x
umask 077

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
backend_root="$(cd "${script_dir}/.." && pwd)"
frontend_root="${INBOX_FRONTEND_ROOT:-$(cd "${backend_root}/../ind-hub-app-ngx-pri-gh" && pwd)}"
required_node="$(<"${backend_root}/.nvmrc")"

for command_name in node npm go golangci-lint postman jq; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    echo "Error: falta ${command_name}." >&2
    exit 1
  }
done

if [[ "$(node --version)" != "${required_node}" ]]; then
  echo "Error: API Inbox requiere Node ${required_node}; actual $(node --version)." >&2
  exit 1
fi
if [[ ! -f "${frontend_root}/package.json" ]]; then
  echo "Error: no se encontro el frontend en ${frontend_root}." >&2
  exit 1
fi

cd "${backend_root}"
if ! git diff --quiet HEAD -- services/api-inbox/domain/agent.go services/api-inbox/domain/agent_tools.go services/api-inbox/functions/agent_worker.go; then
  echo "Error: hay cambios en la implementacion protegida del agente IA." >&2
  exit 1
fi

bash scripts/validate-api-inbox-contracts.sh
node --test scripts/collect-api-inbox-p4-evidence.test.mjs
postman collection lint postman/collections/api-inbox-p4-e2e
postman collection lint postman/collections/api-inbox-p4-local-resilience
postman environment lint postman/environments/inbox-dev.environment.yaml --fail-severity warning
bash scripts/test-api-inbox-p4-harness.sh
go test -race ./services/api-inbox/...
go vet ./services/api-inbox/...
golangci-lint run ./services/api-inbox/...
make build service=services/api-inbox

cd "${frontend_root}"
npm run check:buttons
npm run test:inbox:browser
npm run build

cd "${backend_root}"
git diff --check
if pgrep -f 'postman mock run postman/mocks/api-inbox-p4' >/dev/null; then
  echo "Error: quedo un mock Postman activo." >&2
  exit 1
fi

echo "Readiness local API Inbox aprobado. Esto no valida DEV, PRD, Meta ni AWS desplegado."
