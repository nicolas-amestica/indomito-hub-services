# Informe: 403 Forbidden en GET /configuracion/anfitriones (dev)

Fecha: 2026-10-12. Ambiente: dev (perfil `pa-dev`, `us-east-1`, cuenta 382670112717). No se tocó `pa-prd`/prd ni repos legacy. Sin commits ni push.

## Resultado

Corregido en dev. El endpoint ya no responde 403: con un token cuyo claim `access` refleja el módulo CONFIGURATION actual responde **HTTP 200**. Falta solo que el usuario cierre sesión e inicie sesión de nuevo (ver Pendientes).

## Causa raíz

El 403 lo generaba el Lambda Authorizer (`iam-authorizer-dev`), no el handler. El JWT del usuario era válido, pero su claim `access` no incluía `/configuracion/anfitriones`, porque el módulo `CONFIGURATION` de la tabla de usuarios (`ind-dev-usuarios-ddb-dev-pri-use1`) solo registraba `/configuracion/tributaria` y `/configuracion/acceso-maestro`. Las rutas de anfitriones se habían desplegado (commit `c52fbb6`) sin registrar su endpoint en ese módulo.

Evidencia de CloudWatch (antes del fix, peticiones del navegador del usuario):

- Gateway, log group `/aws/apigateway/indomito-hub-api-dev`: `GET /configuracion/anfitriones` con `status 403`, `integrationStatus "-"`, `integrationLatency "-"` (el backend nunca se invocó). RequestIds `FJleJhWgIAMEVgw=` (21:22:21Z), `FJmXQiRcIAMEVSw=` (21:28:26Z), `FJmhshdHoAMEVpw=` (21:29:33Z), `FJmwhiAjIAMEVZA=` (21:31:08Z).
- Authorizer, `/aws/lambda/iam-authorizer-dev`: mensaje `[AUTH][REQUEST] Denied: no autorizacion para metodo GET` y policy `Deny` sobre `arn:aws:execute-api:us-east-1:382670112717:1f4sn5utdi/$default/GET/configuracion/anfitriones`. El mismo token recibió `Allow` en `/configuracion/tributaria`, `/configuracion/acceso-maestro` y `/auth/permisos` en el mismo segundo. El mensaje "no autorizacion para metodo" es el mismo para denegación por método o por endpoint.
- Lambda del servicio, `/aws/lambda/api-catalog-dev-fn-obtener-anfitriones-v1`: sin invocaciones en esas peticiones.
- Mecanismo: `authorize.ts` filtra `access` con `methodArn.includes(entry.endpoint)`; si el token trae `access` y no hay coincidencia, devuelve Deny.

Descartado: allowances insuficientes (ADMIN tiene `crud` en CONFIGURATION), ruta no desplegada (existe en el Gateway con el authorizer), token inválido (el mismo token pasó otras rutas), regla del handler (`list()` no valida rol).

## Archivos modificados por repo

- `ind-hub-api-gox-sls-pri-gh`: nuevo `scripts/configure-meeting-hosts-iam-dev.sh` (sin versionar aún; idempotente, fijo a `pa-dev`/`us-east-1`/stack `indomito-hub-infra-ddb-dev`, usa `get-item` y `update-item`, sin Scan). También `.agents/tasks/*` (estado de tarea, no versionable).
- `ind-hub-iam-tsx-sls-pri-gh`, `ind-hub-inf-aws-sls-pri-gh`, `ind-hub-app-ngx-pri-gh`: sin cambios (`git status` limpio).

Dato modificado (no es archivo): ítem `pk=APP#INDOMITO_HUB`, `sk=APP#INDOMITO_HUB#LV1#ADM#LV2#CONFIGURATION` de `ind-dev-usuarios-ddb-dev-pri-use1`; `endpoints` ahora es `[/configuracion/tributaria, /configuracion/acceso-maestro, /configuracion/anfitriones]`.

## Comandos de despliegue ejecutados (solo dev)

- `bash scripts/configure-meeting-hosts-iam-dev.sh` (dos veces: la primera agregó el endpoint, la segunda confirmó idempotencia sin duplicados). Es una escritura en DynamoDB dev, no un despliegue.
- No se desplegaron authorizer, api-gateway ni services (ya estaban desplegados y no hay cambios de código).

## Verificación con ejecución real

Antes: `aws sts get-caller-identity --profile pa-dev` → cuenta 382670112717. Los tokens se generaron localmente con el secreto de firma de dev y no se imprimieron.

Peticiones `GET https://api.dev.girasindomito.cl/configuracion/anfitriones` con `Origin: https://nuevo.admin.dev.girasindomito.cl` (2026-10-12 21:40Z):

| Token | HTTP | requestId | Gateway | Authorizer |
| --- | --- | --- | --- | --- |
| Control: `access` previo al fix (sin anfitriones) | **403** | `FJoHngmVoAMEa4A=` | `integrationStatus "-"` | `Denied` + policy `Deny` |
| `access` con los endpoints actuales de CONFIGURATION (incluye anfitriones) | **200** | `FJoHvjzIoAMESWQ=` | `integrationStatus 200`, latencia 1399 ms | policy `Allow` |
| `make token stage=dev allowances=crud` (sin claim `access`) | **200** | `FJoICgTxoAMESaw=` | `integrationStatus 200`, latencia 18 ms | policy `Allow` |

Cuerpo de las respuestas 200: `{"data":{"items":[]}}` (lista vacía, sin anfitriones registrados todavía).

El control prueba que el 403 se reproducía por la falta del endpoint en `access` y que el registro en DynamoDB lo resuelve.

Preflight `OPTIONS /configuracion/anfitriones` con `Origin: https://nuevo.admin.dev.girasindomito.cl`, `Access-Control-Request-Method: GET`: **HTTP 204**, requestId `FJoIHhTpoAMEa6g=`, `access-control-allow-origin: https://nuevo.admin.dev.girasindomito.cl`, `allow-methods: DELETE,GET,OPTIONS,PATCH,POST,PUT`, `allow-headers: authorization,content-type,idempotency-key,x-correlation-id,x-request-id`, `allow-credentials: true`. Las respuestas GET también devuelven el header CORS del origen.

CloudWatch después del fix (ventana 21:40:20Z a 21:40:40Z):

- `/aws/apigateway/indomito-hub-api-dev`: 403 (control) y dos 200 con `integrationStatus 200`, más el 204 del preflight.
- `/aws/lambda/iam-authorizer-dev`: `Deny` para el control (21:40:26Z) y `effect: 'Allow'` para los dos tokens siguientes (21:40:26Z y 21:40:28Z).
- `/aws/lambda/api-catalog-dev-fn-obtener-anfitriones-v1`: dos invocaciones completas (RequestIds `7f68baf6-...` con cold start y `dfc3d0e2-...`), ambas con `END`/`REPORT`. En el control no hubo invocación.

Limpieza: se eliminaron los tokens (`/tmp/tok_*.txt`), los cuerpos de respuesta (`/tmp/body_*.txt`) y el script temporal de generación de tokens en `ind-hub-iam-tsx-sls-pri-gh/scripts/`. No se descargaron logs a disco.

## Límites de la verificación

- No se hizo login real con el usuario del navegador (no hay credenciales); el token de prueba con `access` es sintético y replica los endpoints guardados en DynamoDB. No se confirmó el `profileCode` del usuario real (se asume ADMIN).
- El token de `make token` no lleva `access`, por lo que solo prueba que la ruta está operativa, no el fix. La prueba del fix es el par control/post-fix.

## Pendientes para el usuario

1. **Cerrar sesión e iniciar sesión de nuevo** en `nuevo.admin.dev.girasindomito.cl`: `access` va dentro del JWT (vigencia 8 h) y el token actual no se actualiza solo. Si persiste el 403 tras el re-login, revisar `PFL#<perfil>#...CONFIGURATION` del perfil real del usuario.
2. **Decidir el punto 3.2 del plan**: registrar `/configuracion/anfitriones:opciones` en el módulo `MEET_AGENDA` para que la Agenda lea las opciones sin acceso a Configuración. No se aplicó.
3. **prd**: el mismo 403 aparecerá al desplegar en prd si no se registra el endpoint allí. Requiere decisión y confirmación explícitas; no se hizo.
4. **Commit del script**: `scripts/configure-meeting-hosts-iam-dev.sh` sigue sin versionar. Mensaje sugerido: `fix(configuration): registrar endpoint de anfitriones en IAM dev`.
5. `deploy-dev.yml` no ejecuta los scripts `configure-*`; un dev reconstruido necesitará correrlo a mano.
