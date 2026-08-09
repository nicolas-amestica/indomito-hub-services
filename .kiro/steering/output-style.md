---
inclusion: auto
description: Formato de commits, respuestas y PRs
---

# Output Style — Indomito Hub

> Formato de commits, respuestas y PRs para todos los agentes IA.

## Commits

```text
tipo(scope): descripcion breve en espanol sin tildes (ISSUE-ID)
```

- Max 50 chars (hard limit: 72). Linea en blanco antes del cuerpo.
- Commits en espanol **sin tildes** (limitacion de git log y herramientas CLI). Vinetas con —
- Issue-ID obligatorio: `(IND-1234)` o `(NO-ISSUE)` si no hay ticket
- Proyecto: **IND**

### Tipos

`feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`

### Scopes

| Repo           | Scopes                                                                       |
| ----------------| ------------------------------------------------------------------------------|
| infrastructure | `ddb`, `s3`, `sqs`, `ssm`, `cdn`, `config`, `deps`                           |
| application    | `giras`, `artistas`, `eventos`, `auth`, `shared`, `layout`, `config`, `deps` |
| services       | `giras`, `artistas`, `eventos`, `auth`, `config`, `storage`, `infra`, `deps` |
| orchestrator   | `docs`, `tools`, `steering`, `workspace`, `config`                           |

### Ejemplo

```text
feat(auth): agregar endpoint de renovacion de token (IND-0042)

— Agrega handler fn-renovar-token-v1
— Valida refresh token y emite nuevo access token
```

## Respuestas

- Espanol correcto con tildes y acentos. Directo y conciso.
- Codigo completo y funcional. Comentarios solo si aportan valor.
- Archivos de codigo: ingles. Documentacion: espanol correcto. API paths: espanol plural kebab-case sin tildes.

## Pull Requests

Titulo: `tipo(scope): descripcion breve sin tildes (ISSUE-ID)` (max 70 chars). Descripcion (body): espanol correcto con tildes. Resumen, que se probo, pendientes.
