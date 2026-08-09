---
inclusion: auto
description: Stack, paradigma endpoint-per-function y reglas criticas del backend Go
---

# api-gox-gh — Contexto Local

- **Stack**: Go 1.25, Echo v4, DynamoDB, Serverless Framework v4, Lambda ZIP ARM64
- **Arquitectura**: Multi-servicio con Go workspace (`go.work`). Cada servicio es un modulo Go independiente.
- **Responsabilidad**: API de autenticacion, giras, artistas, eventos
- **Patron referencia**: `services/api-auth/`

## Paradigma: Endpoint-per-Function

- Cada endpoint Lambda tiene su propio entrypoint (`cmd/fn-<endpoint>-v1/main.go`) y logica en `functions/<endpoint>/endpoint.go`
- `endpoint.go` exporta `Register(e *echo.Echo, logger *zap.Logger)` y `Handle(ctx, req) (resp, error)`
- `ctx context.Context` como primer argumento
- Sin `type Service struct` ni receptores para agrupar funciones sin estado real

## Estructura de un Servicio

```
services/api-<nombre>/
├── cmd/
│   ├── fn-<endpoint>-v1/main.go   — Entrypoint Lambda
│   └── local-api/main.go          — Entrypoint local
├── functions/
│   ├── app.go                     — Bootstrap
│   ├── config.go                  — Configuracion
│   ├── routes.go                  — Rutas y servidor local
│   └── <endpoint>/endpoint.go     — Handler (Register + Handle)
├── domain/                        — Modelos de dominio y logica de negocio
│   └── <subdominio>/
├── serverless.ts                  — Config Serverless Framework
└── service.config.json            — Metadata del servicio
```

## Reglas Criticas

- Logging: `zap` estructurado. Prohibido `fmt.Println`.
- Godoc obligatorio en funciones publicas de `functions/` y `domain/`. Godoc en ingles.
- No usar directorios `store/` ni `repositories/`. Persistencia directa en `functions/` o `domain/<subdominio>/datasources/`.
- API REST: ruta base `/v1/<nombre-servicio>`, espanol sin acentos, plural, kebab-case.
- Desarrollo: `make dev service=services/api-<nombre>`. Build: `make build service=services/api-<nombre>`.
- Region unica: us-east-1 (Norte de Virginia).
