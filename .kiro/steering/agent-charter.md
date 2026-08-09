---
inclusion: auto
description: Reglas de rol, idioma y seguridad para agentes IA
---

# Agent Charter — Indomito Hub

> Reglas esenciales de rol, idioma y seguridad para todos los agentes IA.

## Contexto

- **Producto**: Indomito Hub — plataforma de gestion de giras, artistas y eventos musicales
- **Arquitectura**: multi-repo con orquestador centralizado (`orc-hub-gh`)
- **Repos**: `app-ngx-gh` (Angular), `api-gox-gh` (Go servicios), `ifr-sls-gh` (infra AWS)

## Comportamiento

1. Fuente de verdad local: `AGENTS.md` en cada repo. Adaptadores por agente referencian, no duplican.
2. No duplicar reglas documentadas en estandares o steering files.
3. Jerarquia: `docs/standards/` > `ai/source/` > `<repo>/docs/ai/` > `<repo>/AGENTS.md`
4. Archivos `AUTO-GENERATED`: no editar. Fuente en `ai/source/`, sync con `sync-context.sh`.

## Idioma

- Documentacion: espanol correcto con tildes y acentos.
- Identificadores (variables, funciones, clases, archivos): ingles
- Rutas/URLs: espanol kebab-case sin tildes (`/giras-programadas`, `/artistas-invitados`)
- Textos UI: espanol correcto con tildes
- Godoc: ingles (backend Go)
- TSDoc: espanol (frontend)
- Commits: espanol sin tildes (limitacion de git log y herramientas CLI)

## Seguridad

- Auth: A definir (OAuth2/JWT)
- Credenciales: AWS Secrets Manager, nunca hardcodeadas
- IDs: UUIDs v7
- Configuraciones de formularios: en BD (DynamoDB), no hardcodeadas en frontend

## Patrones de Referencia

| Repo       | Modulo referencia   | Paradigma             |
| ---------- | ------------------- | --------------------- |
| app-ngx-gh | (por definir)       | Stores, servicios     |
| api-gox-gh | services/api-auth/  | Endpoint-per-function |
| ifr-sls-gh | ddb/, s3/, common/  | Modulos Serverless    |
