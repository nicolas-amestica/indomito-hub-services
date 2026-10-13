# Plan: 403 Forbidden en GET /configuracion/anfitriones (dev)

## 1. Causa raíz (verificada con ejecución real en pa-dev / us-east-1)

El 403 lo genera el **Lambda Authorizer** (`iam-authorizer-dev`), no el handler. El token del
usuario es válido (firma correcta), pero su claim `access` **no contiene la ruta
`/configuracion/anfitriones`**, porque el módulo `CONFIGURATION` en la tabla de usuarios de
DynamoDB dev no registra ese endpoint.

### Evidencia

| Hecho | Fuente | Resultado |
| --- | --- | --- |
| Cuenta correcta | `aws sts get-caller-identity --profile pa-dev` | Account 382670112717 |
| La ruta existe y está desplegada en el Gateway compartido | `apigatewayv2 get-routes --api-id 1f4sn5utdi` | `GET /configuracion/anfitriones` (y POST, PUT/DELETE `/{id}`, `GET ...:opciones`) con `AuthorizationType=CUSTOM`, `AuthorizerId=d3or7i` (`iamAuth`, REQUEST, TTL 0) |
| Access log del Gateway | `/aws/apigateway/indomito-hub-api-dev` | `GET /configuracion/anfitriones` → `status 403`, `integrationStatus "-"`, `integrationLatency "-"` (el backend nunca se invoca). Request IDs: `FJleJhWgIAMEVgw=` (2026-10-12 21:22:21Z), `FJmXQiRcIAMEVSw=` (21:28:26Z), `FJmhshdHoAMEVpw=` (21:29:33Z), `FJmwhiAjIAMEVZA=` (21:31:08Z) |
| El authorizer denegó | `/aws/lambda/iam-authorizer-dev` | `[AUTH][REQUEST] Denied: no autorizacion para metodo GET` en 21:22:21.521Z, 21:28:27.844Z, 21:29:33.608Z, 21:31:08.442Z; policy `Deny` sobre `arn:aws:execute-api:us-east-1:382670112717:1f4sn5utdi/$default/GET/configuracion/anfitriones` |
| El mismo token pasó otras rutas en el mismo segundo | mismo log group, 21:31:08 | `Allow` en `GET /configuracion/tributaria`, `GET /configuracion/acceso-maestro`, `GET /auth/permisos` → token válido, solo falta la ruta |
| La Lambda del servicio no se ejecutó | `aws logs tail /aws/lambda/api-catalog-dev-fn-obtener-anfitriones-v1 --since 24h` | Sin eventos |
| Causa en datos | Query a `ind-dev-usuarios-ddb-dev-pri-use1`, `pk=APP#INDOMITO_HUB` | Módulo `CONFIGURATION` → `endpoints: ["/configuracion/tributaria", "/configuracion/acceso-maestro"]`. No incluye anfitriones. Ningún otro módulo lo incluye |
| El perfil ADMIN sí tiene permisos sobre el módulo | Query `pk=APP#PROFILE#PERMISSIONS`, `begins_with(sk,"PFL#ADMIN#")` | `...LV2#CONFIGURATION` → `c,r,u,d` (el problema no son los allowances) |

### Mecanismo

1. `ind-hub-api-gox-sls-pri-gh/services/api-identity/functions/handler.go` (`signToken`) arma `access = [{endpoint, allowances}]` a partir de `Module.Endpoints` de cada módulo permitido al perfil.
2. `ind-hub-iam-tsx-sls-pri-gh/src/functions/authorize.ts` (`generateCustomTokenPolicy`) busca `access` con `methodArn.includes(entry.endpoint)`. Si el token trae `access` y no hay coincidencia, `accessToEndpoint = "Deny"` y se registra "Denied: no autorizacion para metodo GET" (el mensaje es el mismo para denegación por método o por endpoint, de ahí lo engañoso del log).
3. Las rutas de anfitriones se agregaron al código y se desplegaron (commit `c52fbb6`, 2026-10-12) sin registrar su endpoint en el módulo `CONFIGURATION`. Es el mismo patrón que ya se resolvió con `scripts/configure-payment-master-access-iam-dev.sh` para `/configuracion/acceso-maestro`.

### Causas descartadas

- (a) allowances insuficientes: ADMIN tiene `crud` en CONFIGURATION.
- (b) ruta inexistente o no desplegada: la ruta existe en el Gateway con el authorizer asociado y la integración apunta a la Lambda.
- (c) token ausente, expirado o mal firmado: el mismo token fue aceptado en otras rutas; el mensaje "Invalid custom token signature" no aparece.
- (d) regla de dominio del handler: `list()` en `services/api-catalog/functions/anfitriones/endpoint.go` no hace chequeo de rol (solo create/update/remove exigen ADMIN/ADM) y la Lambda no recibe invocaciones.
- (e) falta de `public: true` / permisos de invocación: las rutas son privadas por diseño (`public:false` en `services/api-catalog/serverless.ts`) y el authorizer sí se invoca.

### Frontend (sin cambios necesarios)

`ind-hub-app-ngx-pri-gh/src/app/core/configuration/meeting-host.service.ts` llama `GET ${environment.apiUrl}/configuracion/anfitriones`; `core/auth/auth.interceptor.ts` agrega `Authorization: Bearer <token>`. La URL y el método son correctos.

## 2. Repo donde corregir

La causa es de **datos de permisos (DynamoDB dev, tabla de usuarios)**, no de código de ruta, authorizer ni frontend. Según la tabla de routing, lo que cambia es la configuración de módulos IAM que gestiona `services` (api-identity), por lo que el cambio versionado va en **`ind-hub-api-gox-sls-pri-gh`** (script `scripts/`, igual que sus scripts `configure-*-iam-dev.sh`). No hay cambios en `authorizer`, `infrastructure` ni `app-ngx-hub`.

## 3. Cambio mínimo propuesto

### 3.1 Nuevo script `scripts/configure-meeting-hosts-iam-dev.sh` (obligatorio)

Clonar el patrón de `scripts/configure-payment-master-access-iam-dev.sh` (perfil `pa-dev`, región `us-east-1`, stack `indomito-hub-infra-ddb-dev`, output `UsuariosTableName`, operación idempotente):

- `MODULE_SK="APP#INDOMITO_HUB#LV1#ADM#LV2#CONFIGURATION"`, `ENDPOINT="/configuracion/anfitriones"`.
- Si `length(Item.endpoints.L[?S=='/configuracion/anfitriones'])` es 0, hacer `update-item` con `list_append(if_not_exists(endpoints, :empty), :endpoint)` y `condition-expression attribute_exists(pk) AND attribute_exists(sk)`.
- No tocar `allowances`: ADMIN ya tiene `c,r,u,d` en CONFIGURATION (verificado).
- Mensaje final: cerrar sesión y volver a ingresar para emitir un token nuevo.

Una sola entrada cubre las cinco rutas, porque el authorizer compara con `methodArn.includes(endpoint)`: `/configuracion/anfitriones`, `...:opciones` y `.../{id}`.

### 3.2 Entrada adicional para MEET_AGENDA (recomendada, requiere confirmación del usuario)

`meet-calendar.page.ts` consume `GET /configuracion/anfitriones:opciones`. Hoy un usuario con acceso a Agenda pero sin Configuración quedaría con 403 también ahí. Propuesta: en el mismo script, agregar `/configuracion/anfitriones:opciones` al módulo `...#LV2#MEET_AGENDA`. El authorizer elige la entrada más específica (`sort` por longitud de endpoint), así que `:opciones` usaría los allowances de MEET_AGENDA y el resto seguiría con los de CONFIGURATION.
**Decisión pendiente del usuario**: ¿se quiere que Agenda lea las opciones sin acceso a Configuración? Si no, omitir 3.2.

### 3.3 Sin cambios

No refactorizar `signToken`, el authorizer, `routes.go` ni el frontend.

### 3.4 Fuera de alcance / pendiente de decisión (prd)

Este plan solo toca dev (`pa-dev`). El script debe fallar o no ejecutarse contra prd. El registro equivalente en prd es una decisión aparte y requiere confirmación explícita; no se hizo nada con `pa-prd`.

## 4. Despliegue

**No requiere desplegar** authorizer, infrastructure/api-gateway ni services. El código y las rutas ya están desplegados en dev. El cambio es un `update-item` sobre la tabla de usuarios dev.

Orden de ejecución:
1. `aws sts get-caller-identity --profile pa-dev` (confirmar cuenta 382670112717).
2. `bash scripts/configure-meeting-hosts-iam-dev.sh` (escritura en DynamoDB dev, acotada a 1 o 2 ítems de módulo; reversible quitando el elemento de la lista `endpoints`). Es una acción sobre un recurso compartido de dev: ejecutarla solo tras la aprobación del usuario.
3. El usuario debe **cerrar sesión e iniciar sesión de nuevo**: `access` va dentro del JWT (vigencia 8 h, `exp = iat + 28800`), así que el token vigente no se actualiza solo.

Nota: como las rutas están en `deploy-dev.yml` pero ese workflow no ejecuta los scripts `configure-*`, conviene documentar la ejecución del script en el mensaje del commit/PR. Agregarlo a CI queda fuera del alcance mínimo.

## 5. Verificación

1. Antes de ejecutar, comprobar (Query, nunca Scan) que el módulo CONFIGURATION no tiene el endpoint:
   `aws dynamodb get-item --table-name ind-dev-usuarios-ddb-dev-pri-use1 --key '{"pk":{"S":"APP#INDOMITO_HUB"},"sk":{"S":"APP#INDOMITO_HUB#LV1#ADM#LV2#CONFIGURATION"}}' --projection-expression endpoints --profile pa-dev --region us-east-1`
2. Después de ejecutar, repetir el comando: debe incluir `/configuracion/anfitriones`.
3. Ejecutar el script una segunda vez: no debe duplicar el endpoint (idempotencia).
4. Con sesión nueva en `nuevo.admin.dev.girasindomito.cl` → Configuración → Anfitriones: el listado carga sin 403.
5. CloudWatch: en `/aws/apigateway/indomito-hub-api-dev` el `GET /configuracion/anfitriones` debe mostrar `status 200` con `integrationStatus 200`; en `/aws/lambda/iam-authorizer-dev` debe aparecer `effect: 'Allow'` para ese `methodArn`; y `/aws/lambda/api-catalog-dev-fn-obtener-anfitriones-v1` debe tener invocaciones.
6. Si se aplica 3.2: en Agenda de videollamadas, `GET ...:opciones` responde 200.
7. Si sigue el 403 tras iniciar sesión de nuevo: confirmar con `aws logs filter-log-events` que el nuevo token se emite (login) y revisar que `ADMIN` sea realmente el `profileCode` del usuario.

## 6. Qué no se pudo verificar (needs verification during implementation)

- **No se reprodujo con curl ni se emitió un token**: `scripts/mint-token.ts` emite un JWT sin claim `access` (si aplica `endpoints` indefinido, el authorizer permite), por lo que no reproduciría la denegación, y no se leyeron secretos SSM. La evidencia es de los logs reales del navegador del usuario (IP 190.114.40.4).
- No se decodificó el JWT del usuario, así que el contenido exacto de su claim `access` se **infiere** del código y de los datos de DynamoDB, no se observó directamente.
- No se confirmó cuál es el `profileCode` del usuario que está logueado (se asume `ADMIN`; si es otro perfil, habría que revisar `PFL#<perfil>#...CONFIGURATION`).
- No se verificó el estado de prd (intencionalmente no se accedió a `pa-prd`).

## 7. Qué falta del usuario

- Confirmar si se aplica 3.2 (Agenda lee `:opciones`).
- Aprobar la escritura en DynamoDB dev (paso 2 del despliegue).
- Decidir si prd se corrige después, en otro paso.
- No se necesitan valores de secretos SSM ni roles nuevos.

## 8. Commit (cuando se implemente)

Un solo commit en `ind-hub-api-gox-sls-pri-gh`, scope `configuration`:
`fix(configuration): registrar endpoint de anfitriones en IAM dev`
