#!/bin/bash
set -euo pipefail

SERVICES=(
  api-catalog
  api-contract
  api-identity
  api-program
  api-payment
  payment-receipts
)

for service in "${SERVICES[@]}"; do
  echo "Deploying services/$service"
  npx tsx scripts/deploy-service.ts \
    --service "services/$service" \
    --stage dev \
    --region us-east-1
done
