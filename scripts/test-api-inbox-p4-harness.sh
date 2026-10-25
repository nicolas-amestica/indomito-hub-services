#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
collection="postman/collections/api-inbox-p4-e2e"
resilience_collection="postman/collections/api-inbox-p4-local-resilience"
mock="postman/mocks/api-inbox-p4"
dataset="postman/data/inbox-p4-media.mock.json"
mock_mapping="{{base_url}} mock:${mock}"

command -v postman >/dev/null 2>&1 || {
  echo "Error: Postman CLI no esta instalado o no esta en PATH." >&2
  exit 1
}

for required_path in "${collection}" "${resilience_collection}" "${mock}" "${dataset}" "postman/fixtures/p4-media-fixture.bin"; do
  if [[ ! -e "${repo_root}/${required_path}" ]]; then
    echo "Error: falta ${required_path}." >&2
    exit 1
  fi
done

cd "${repo_root}"

echo "Ejecutando capacidades JSON contra simulacion local..."
postman collection run "${collection}" \
  --use-mock "${mock_mapping}" \
  --env-var access_token=local-test-token \
  --env-var phone=56911111111 \
  --env-var reaction_message_id=01K8P4MOCK0000000000000000 \
  -i 'Listar conversaciones' \
  -i 'Enviar texto' \
  -i 'Enviar plantilla aprobada' \
  -i 'Enviar ubicación' \
  -i 'Enviar contacto' \
  -i 'Enviar mensaje interactivo' \
  -i 'Enviar reacción' \
  -i 'Verificar conversación' \
  --bail \
  --no-report-events

echo "Ejecutando 13 iteraciones multimedia contra simulacion local..."
postman collection run "${collection}" \
  --use-mock "${mock_mapping}" \
  --env-var access_token=local-test-token \
  --env-var phone=56911111111 \
  -d "${dataset}" \
  -i 'Preparar multimedia' \
  -i 'Subir multimedia a URL firmada' \
  -i 'Confirmar envío multimedia' \
  --bail \
  --no-report-events \
  --working-dir "${repo_root}"

echo "Ejecutando errores, idempotencia y reintentos contra simulacion local..."
postman collection run "${resilience_collection}" \
  --use-mock "${mock_mapping}" \
  --bail \
  --no-report-events \
  --working-dir "${repo_root}"

echo "Arnes local P4 aprobado: 8 requests JSON, 39 requests multimedia y 19 casos de resiliencia."
