---
inclusion: auto
description: Reglas de rol, idioma y seguridad para agentes IA
---

# Agent Charter — Indómito Hub

> Reglas esenciales de rol, idioma y seguridad para todos los agentes IA.

## Contexto

- **Producto**: Indómito Hub — plataforma de gestión de viajes con especialización en giras de estudios
- **Arquitectura**: multi-repo con orquestador centralizado (`orchestrator`)
- **Repos**: `application` (Angular), `services` (Go microservicios), `infrastructure` (Infraestructura AWS)
- **Región AWS**: us-east-1 (Norte de Virginia) para todos los servicios
- **Perfiles AWS**: `pa-dev` (desarrollo), `pa-prd` (producción)
- **Ambientes**: dev y prd (sin QA)

## Comportamiento

1. Fuente de verdad local: `AGENTS.md` en cada repo. Adaptadores por agente referencian, no duplican.
2. No duplicar reglas documentadas en estándares o steering files.
3. Jerarquía: `docs/standards/` > `ai/source/` > `<repo>/docs/ai/` > `<repo>/AGENTS.md`
4. Archivos `AUTO-GENERATED`: no editar. Fuente en `ai/source/`, sync con `sync-context.sh`.

## Idioma

- Documentación: español correcto con tildes y acentos.
- Identificadores (variables, funciones, clases, archivos): inglés
- Rutas/URLs: español kebab-case sin tildes (`/viajes`, `/pasajeros`, `/cotizaciones`)
- Textos UI: español correcto con tildes
- TSDoc: español (frontend y backend Go)
- Godoc: español
- Commits: español sin tildes (limitación de git log y herramientas CLI)

## Seguridad

- Auth: JWT custom (autenticador propio, `job-authorizer`)
- Credenciales: AWS Secrets Manager / SSM Parameter Store, nunca hardcodeadas
- IDs: ULID
- Configuraciones dinámicas: en BD (DynamoDB), no hardcodeadas en frontend
- Email: Nodemailer (no SES)

## Patrones de Referencia

| Repo           | Módulo referencia        | Paradigma             |
| ----------------| --------------------------| -----------------------|
| application    | `src/app/` (por definir) | Stores, servicios     |
| services       | `services/api-auth/`     | Endpoint-per-function |
| infrastructure | `ddb/`, `s3/`, `common/` | Módulos Serverless    |
