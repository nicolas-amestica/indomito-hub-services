---
inclusion: auto
description: Formato de commits, respuestas y PRs
---

# Output Style — Indómito Hub

> Formato de commits, respuestas y PRs para todos los agentes IA.

## Commits

```text
tipo(scope): descripcion breve en espanol sin tildes
```

- Max 50 chars (hard limit: 72). Línea en blanco antes del cuerpo.
- Commits en español **sin tildes** (limitación de git log y herramientas CLI). Viñetas con —
- Sin gestión de tickets externa. No se requiere Issue-ID.

### Tipos

`feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`

### Scopes

| Repo           | Scopes                                                                                   |
| -------------- | ---------------------------------------------------------------------------------------- |
| infrastructure | `ddb`, `s3`, `ssm`, `cdn`, `waf`, `iot`, `config`, `deps`                               |
| application    | `core`, `app-auth`, `accounting`, `analytics`, `assign-installment`, `shared`, `client`, `configuration`, `documents`, `help`, `home`, `inbox`, `informative-media`, `layout`, `meet`, `passenger`, `payment`, `payment-history`, `profile`, `program`, `ticket`, `tools` |
| services       | `accounting`, `balance`, `configuration`, `contract`, `entity`, `extraction`, `favorites`, `maintainer`, `meet`, `notification`, `payment`, `program`, `ticket`, `tools`, `whatsapp-agent`, `authorizer`, `trigger`, `config`, `deps` |
| orchestrator   | `docs`, `tools`, `steering`, `workspace`, `config`                                       |

### Ejemplo

```text
feat(viajes): agregar listado de viajes activos

— Agrega vista con filtro por estado en la pagina de viajes
```

## Respuestas

- Español correcto con tildes y acentos. Directo y conciso.
- Código completo y funcional. Comentarios solo si aportan valor.
- Archivos de código: inglés. Documentación: español correcto. API paths: español plural kebab-case sin tildes.

## Pull Requests

Título: `tipo(scope): descripcion breve sin tildes` (max 70 chars). Descripción (body): español correcto con tildes. Resumen, qué se probó, pendientes.
