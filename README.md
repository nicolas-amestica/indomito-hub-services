# Indómito Hub — Backend (Go)

Microservicios serverless en Go desplegados en AWS Lambda (ARM64), con
Serverless Framework v4. Paradigma **endpoint-per-function**: cada endpoint
HTTP compila a su propio binario y su propia función Lambda.

> **Estado actual**: `services/` está vacío. Los endpoints de prueba
> (`api-auth` con `hello-world-v1`, e `iam-auth` como Lambda Authorizer) se
> eliminaron el 2026-08-31 para construir los endpoints reales desde cero.
> La infraestructura compartida (Gateway, dominio, DNS) sigue desplegada y
> lista — ver [Crear un servicio nuevo](#crear-un-servicio-nuevo).

## Stack

- Go 1.25 (Go workspace, un módulo por microservicio)
- Echo v4 (solo para el servidor local de desarrollo)
- DynamoDB
- AWS Lambda `provided.al2023`, arquitectura ARM64
- Serverless Framework v4 (configuración en TypeScript)
- `zap` para logging estructurado
- Región: us-east-1

## Estructura

```text
.
├── services/           — Microservicios (cada uno es un modulo Go independiente)
├── bootstrap/          — Inicializacion compartida: config AWS, env helpers
│   ├── app.go
│   ├── aws.go
│   └── env.go
├── common/             — Config compartida de Serverless (TypeScript)
│   ├── go-service.ts        — Builder del serverless.ts de cada servicio
│   ├── api-services.ts      — Registro de nombres de servicios
│   ├── custom-parameters.ts — Stage, region, bucket de deploys
│   ├── environment.ts       — Resolucion del stage (dev | prd)
│   ├── aws-regions-allowed.ts
│   └── aws-service-tags.ts
├── aws/policies/       — Builders de politicas IAM reutilizables
├── configs/            — Variables de entorno para desarrollo local
├── scripts/            — build, validate, deploy y dev server
├── go.work             — Workspace Go (declarar aqui cada servicio nuevo)
└── Makefile            — Comandos de desarrollo y despliegue
```

## Requisitos

- Go 1.25+
- Node.js 24.15.0 (ver `.nvmrc`)
- AWS CLI con perfiles SSO `pa-dev` y `pa-prd`
- `zip` (usado por el build para empaquetar el binario)

```bash
make install    # instala dependencias Node
```

## Arquitectura: un solo API Gateway compartido

Los microservicios **no crean su propio API Gateway**. Todos registran sus
rutas en un único HTTP API Gateway v2 compartido, desplegado por el repo de
infraestructura (`ind-hub-inf-aws-sls-pri-gh/api-gateway/`).

Esto permite exponer un solo dominio sin prefijo por servicio:

| Ambiente | Dominio de la API |
| -------- | ------------------------- |
| dev      | api.dev.girasindomito.cl  |
| prd      | api.girasindomito.cl      |

`common/go-service.ts` referencia ese Gateway por su ID
(`provider.httpApi.id`). Por eso **CORS, throttling, el dominio personalizado
y el Lambda Authorizer se configuran una sola vez en infraestructura**, no en
cada servicio.

### Authorizer: desactivado por ahora

El Gateway compartido no tiene Lambda Authorizer desplegado
(`authorizerEnabled: false` en
`ind-hub-inf-aws-sls-pri-gh/api-gateway/api-gateway-config.ts`).

Mientras siga así, **todo endpoint debe declararse `public: true`**. Si se
define un endpoint protegido, el build falla con un mensaje explicando las
opciones (ver `SHARED_AUTHORIZER_ENABLED` en `common/go-service.ts`).

Para reactivar la autorización:

1. Crear el servicio authorizer en `services/`.
2. Desplegarlo.
3. Poner `authorizerEnabled: true` en infra y desplegar `api-gateway`.
4. Poner `SHARED_AUTHORIZER_ENABLED = true` en `common/go-service.ts`.

## Crear un servicio nuevo

Ejemplo para `api-viajes` con un endpoint `POST /viajes`.

**1. Estructura de archivos**

```text
services/api-viajes/
├── cmd/
│   ├── fn-crear-viaje-v1/main.go   — Entrypoint Lambda del endpoint
│   └── local-api/main.go           — Servidor local (Echo)
├── functions/
│   ├── app.go                      — Bootstrap del servicio
│   ├── config.go                   — Configuracion
│   ├── routes.go                   — RunLocal + registro de rutas locales
│   └── crear-viaje-v1/endpoint.go  — Handler (Register + Handle)
├── domain/                         — Modelos de dominio
├── go.mod
├── serverless.ts
└── service.config.json
```

**2. `serverless.ts`**

```ts
import { ApiServices } from '../../common/api-services';
import { buildGoServiceServerless } from '../../common/go-service';

const endpoints = [
  { name: 'crear-viaje-v1', method: 'POST', path: '/viajes', public: true },
] as const;

const config = buildGoServiceServerless(ApiServices.Viajes, [...endpoints]);

module.exports = config;
```

**3. Registrar el servicio** en `common/api-services.ts` y en `go.work`.

**4. Path del endpoint**: español, plural, kebab-case, sin tildes
(`/viajes`, `/cotizaciones`, `/pasajeros:buscar`). Ver
`docs/standards/global/api-design.md` en el orquestador.

> El path en `serverless.ts` (lo que se despliega en AWS) y el path registrado
> en `functions/<endpoint>/endpoint.go` (servidor local) **deben coincidir**.
> Son dos declaraciones distintas y es fácil que se desincronicen.

**5. `service.config.json`**: declarar cada función con su `entrypoint` y las
variables de entorno requeridas (`requiredEnvironment`) — el validador de
deploy falla si falta alguna.

## Comandos

```bash
make dev service=services/api-viajes            # servidor local (Echo)
make build service=services/api-viajes          # compila binarios ARM64 + ZIP
make validate service=services/api-viajes stage=dev
make deploy service=services/api-viajes stage=dev region=us-east-1
make deploy-quick service=services/api-viajes stage=dev   # omite build y validate
make clean service=services/api-viajes
make remove service=services/api-viajes stage=dev region=us-east-1
```

`make deploy` ejecuta: login SSO (si hace falta) → build → validate → deploy.

### Validaciones automáticas

`make validate` corre 14 comprobaciones estructurales antes de desplegar
(runtime, arquitectura, handler, artefactos ZIP, entrypoints, variables de
entorno requeridas). Ver `scripts/validate-service.ts` y
`docs/standards/global/testing-baseline.md` en el orquestador.

## Orden de despliegue

La infraestructura base y el Gateway compartido ya están desplegados. Para un
servicio nuevo basta:

```bash
make deploy service=services/<nombre> stage=dev region=us-east-1
```

Si algún día hay que recrear el Gateway desde cero, el orden completo es:

```text
1. infraestructura base (repo infra): ddb, s3, ssm, cdn
2. servicio authorizer (este repo)          — solo si authorizerEnabled: true
3. api-gateway (repo infra)
4. microservicios (este repo)
```

## Perfiles AWS

| Stage | Profile | Cuenta AWS    |
| ----- | ------- | ------------- |
| dev   | pa-dev  | 382670112717  |
| prd   | pa-prd  | 576394965493  |

## Desarrollo local

No se usa SSM en local: las variables se leen de `configs/.env.local`.

```bash
make dev service=services/<nombre>
```

El servidor local usa Echo y expone las rutas registradas vía `Register()` en
cada `endpoint.go`.

## Convenciones

Las reglas completas están en `AGENTS.md` (generado desde el orquestador).
Resumen de lo específico de este repo:

- Godoc en español; identificadores en inglés.
- Logging con `zap` estructurado. **Prohibido `fmt.Println`.**
- `ctx context.Context` como primer argumento de toda función con I/O.
- IDs: ULIDs.
- **Prohibido `Scan` en DynamoDB** — usar `Query` sobre la key o un GSI.
- Secretos solo vía SSM Parameter Store tipo `SecureString`.
- Sin directorio `store/` ni `repositories/`: la persistencia vive en
  `functions/` o `domain/`.
- Email vía Nodemailer (no SES).
