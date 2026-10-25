#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
cd "${repo_root}"

command -v jq >/dev/null 2>&1 || { echo "Error: falta jq." >&2; exit 1; }
command -v go >/dev/null 2>&1 || { echo "Error: falta go." >&2; exit 1; }

jq -e '.openapi == "3.1.0" and (.paths | type == "object")' services/api-inbox/openapi.fragment.json >/dev/null
jq -e '.asyncapi == "3.0.0" and (.operations | type == "object")' services/api-inbox/asyncapi.json >/dev/null

go test ./services/api-inbox/functions \
  -run 'Test(OpenAPI|AsyncAPI|Serverless)' \
  -count=1

echo "Contratos API Inbox aprobados: OpenAPI, AsyncAPI, rutas, productores e infraestructura."
