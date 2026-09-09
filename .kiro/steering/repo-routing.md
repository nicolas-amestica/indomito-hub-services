<!-- AUTO-GENERATED — DO NOT EDIT MANUALLY -->
<!-- Managed-By: indomito-context-compiler -->
<!-- Artifact-Format: 1 -->
<!-- Engine-Version: 1.0.0 -->
<!-- Source: ai/source/global-context/repo-routing.md -->
---
inclusion: manual
description: Mapa de repos y reglas de routing cross-repo
---

# Repo Routing — Indómito Hub

> Mapa de repositorios y reglas de routing para cambios cross-repo.

## Repos

| Alias          | Stack                               | Responsabilidad                                   |
| -------------- | ----------------------------------- | ------------------------------------------------- |
| infrastructure | TypeScript, Serverless Framework v4 | Infraestructura AWS (DynamoDB, S3, SSM, CDN, API Gateway) |
| application    | Angular 22, Signals, TailwindCSS    | SPA: viajes, cotizaciones, pasajeros, dashboards  |
| services       | Go 1.25, Echo v4, DynamoDB          | Backend: viajes, cotizaciones, contratos, destinos|
| authorizer     | TypeScript, Serverless Framework v4 | Lambda Authorizer compartido (JWT propio HMAC-SHA256) |
| orchestrator   | —                                   | Documentación centralizada, steering, estándares  |

## Repos Legacy

Generación anterior del producto, **hoy en producción**, que Indómito Hub
reemplaza progresivamente. Están en el workspace como referencia funcional y para
mantención correctiva. Region us-west-2, no us-east-1.

| Alias              | Directorio                     | Stack                                          | Reemplazado por            |
| ------------------ | ------------------------------ | ---------------------------------------------- | -------------------------- |
| legacy-application | `portal_admin_ng_dev_pri_usw2` | Angular 21, PrimeNG 21, @ngrx/signals          | application                |
| legacy-services    | `portal-admin-sls-dev-pri-usw2`| Serverless v4: Node.js 22, Go 1.24, Python     | services + authorizer      |

Reglas rápidas:

- Features nuevas **nunca** van a un repo legacy: van a `ind-hub-*`
- Solo se tocan para corregir errores en produccion o cambios normativos urgentes
- No copiar codigo legacy tal cual: reimplementar segun `docs/standards/`
- No participan del sync de contexto IA (no estan en `ai/sync-config.json`)
- Los estandares nuevos no se aplican retroactivamente al legacy
- Verificar `--region us-west-2` y el perfil AWS antes de cualquier comando legacy

Detalle completo en `docs/standards/architecture/legacy-systems.md`.

## Dominios

| Ambiente | Frontend                         | API                                                   |
| -------- | -------------------------------- | ----------------------------------------------------- |
| dev      | nuevo.admin.dev.girasindomito.cl | API Gateway default (asignado automáticamente por AWS)|
| prd      | nuevo.admin.girasindomito.cl     | API Gateway default (asignado automáticamente por AWS)|

## Routing

| Tipo de cambio                             | Repo destino   |
| ------------------------------------------ | -------------- |
| Infraestructura AWS compartida             | infrastructure |
| UI, componentes, stores                    | application    |
| Endpoints, lógica de negocio, API          | services       |
| Validación de tokens, políticas de acceso  | authorizer     |
| Estándares, steering, documentación        | orchestrator   |
| Bug en produccion del portal actual (UI)   | legacy-application |
| Bug en produccion de la API actual         | legacy-services |

## Cambios Cross-Repo

1. **Contratos de API**: documentar en orquestador → implementar backend → consumir frontend
2. **Nuevos módulos**: backend (handler+service+domain) + frontend (store, ruta, componentes)
3. **Auth**: coordinar authorizer (validación de tokens) ↔ services (emisión de tokens) ↔ application (interceptores, guards)
4. **Commits**: siempre separados por repo. Nunca mezclar frontend y backend en un commit.

## Orden de Despliegue del API

El Gateway compartido depende del authorizer, lo que invierte el orden habitual:

1. `authorizer` — exporta el output `iamAuthArn`
2. `infrastructure` — módulo `api-gateway`, que importa ese ARN y crea el authorizer del Gateway
3. `services` — microservicios Go, que registran sus rutas en el Gateway compartido
