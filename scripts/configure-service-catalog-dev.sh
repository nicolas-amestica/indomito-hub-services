#!/usr/bin/env bash
set -euo pipefail

PROFILE="pa-dev"
REGION="us-east-1"
STACK="indomito-hub-infra-ddb-dev"
SCOPE="CTZ"
CATALOG_PK="CAT#SRV#01M4C8DS4AM33H5PN9RTY3372Z"
TABLE="$(aws cloudformation describe-stacks --stack-name "$STACK" --profile "$PROFILE" --region "$REGION" --query "Stacks[0].Outputs[?OutputKey=='CatalogosTableName'].OutputValue | [0]" --output text --no-cli-pager)"

if [[ -z "$TABLE" || "$TABLE" == "None" ]]; then
  echo "No se pudo resolver la tabla de catalogos desde $STACK" >&2
  exit 1
fi

aws dynamodb put-item \
  --table-name "$TABLE" \
  --item "{\"pk\":{\"S\":\"CAT#SRV#SCOPE#$SCOPE\"},\"sk\":{\"S\":\"META\"},\"catalogPk\":{\"S\":\"$CATALOG_PK\"},\"scope\":{\"S\":\"$SCOPE\"},\"description\":{\"S\":\"Relacion directa del catalogo de servicios de cotizacion\"}}" \
  --condition-expression "attribute_not_exists(pk) OR catalogPk = :catalogPk" \
  --expression-attribute-values "{\":catalogPk\":{\"S\":\"$CATALOG_PK\"}}" \
  --profile "$PROFILE" \
  --region "$REGION" \
  --no-cli-pager

echo "Relacion $SCOPE -> $CATALOG_PK configurada en $TABLE."
