# api-catalog

## Siembra de catálogos

`cmd/seed-catalogs` valida un manifiesto JSON y, cuando se agrega `-apply`,
sobrescribe sus claves deterministas con `PutItem`. No usa `Scan`, no elimina el
snapshot de tasas y no borra opciones ajenas al manifiesto.

La validación local no necesita credenciales ni toca AWS:

```bash
go run ./cmd/seed-catalogs -file ./configs/catalog-seed.json
```

La escritura exige tabla y perfil explícitos:

```bash
go run ./cmd/seed-catalogs \
  -file ./configs/catalog-seed.json \
  -table ind-dev-catalogos-ddb-dev-pri-use1 \
  -stage dev \
  -region us-east-1 \
  -profile pa-dev \
  -apply
```

El manifiesto tiene esta forma:

```json
{
  "plans": [
    { "id": "...", "display": "...", "order": 1, "active": true }
  ],
  "seasons": [
    { "id": "...", "display": "...", "order": 1, "active": true }
  ],
  "destinations": [
    {
      "id": "...",
      "display": "...",
      "order": 1,
      "active": true,
      "budgetTemplateId": "..."
    }
  ],
  "settings": {
    "defaultPlanId": "...",
    "margin": {
      "usdIncreaseCLP": 0,
      "brlIncreaseCLP": 0,
      "utilityRate": 0,
      "rechargeRate": 0,
      "minUtilityRate": 0
    },
    "scenarioOffsets": [-10, -5, 0, 5]
  }
}
```

El manifiesto versionado contiene la configuración inicial acordada. Cada
`budgetTemplateId` debe existir en el registro de plantillas de `api-program`;
por ahora todos los destinos seleccionan `brochure-default`.
