---
inclusion: fileMatch
fileMatchPattern: "services/*/functions/*/openapi.ts,services/*/docs/**,docs/openapi/**"
description: Reglas de mantenimiento de documentación OpenAPI
---

# Documentacion OpenAPI — api-gox-gh

## Reglas de mantenimiento

Al crear o modificar un endpoint en este repositorio, mantener la documentacion OpenAPI actualizada:

### 1. Archivo `openapi.ts`

- Cada endpoint debe tener un archivo `openapi.ts` en `services/api-<nombre>/functions/<endpoint>/`
- El archivo exporta un objeto `metadata` con: `tag`, `operationId`, `summary`, `description`, `method`, `path`, `security`, `requestBody`, `responses`, `profiles`
- Al crear un nuevo endpoint, crear el `openapi.ts` correspondiente
- Al modificar la ruta, metodo o parametros de un endpoint, actualizar el `openapi.ts`

### 2. Esquemas Zod

- Los esquemas de request y response se definen en `services/api-<nombre>/docs/schemas/`
- Usar `zod` + `@asteasolutions/zod-to-openapi` para definir los esquemas
- Convencion de nombres: `<operacion>.request.ts`, `<operacion>.response.ts`

### 3. Ejemplos JSON

- Los ejemplos se almacenan en `services/api-<nombre>/docs/examples/`
- Cada endpoint debe tener al menos: request valido, response exitoso, error 400, error 500
- Convencion de nombres: `<operacion>.request.json`, `<operacion>.response-<codigo>.json`

### 4. Regenerar contrato parcial

- Tras modificar `openapi.ts`, esquemas o ejemplos, ejecutar `npm run docs:generate`
- Verificar que el contrato generado en `docs/openapi/<servicio>.openapi.yaml` es correcto

## Estructura de referencia

```text
services/api-<nombre>/
├── docs/
│   ├── schemas/
│   │   ├── <operacion>.request.ts
│   │   └── <operacion>.response.ts
│   └── examples/
│       ├── <operacion>.request.json
│       ├── <operacion>.response-200.json
│       ├── <operacion>.response-400.json
│       └── <operacion>.response-500.json
└── functions/<endpoint>/
    ├── endpoint.go
    └── openapi.ts
```
