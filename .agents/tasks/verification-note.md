# Verification note: 403 en GET /configuracion/anfitriones (dev)

## Cambio
- Nuevo `scripts/configure-meeting-hosts-iam-dev.sh` (clon del patrón de `configure-payment-master-access-iam-dev.sh`; perfil `pa-dev`, `us-east-1`, stack `indomito-hub-infra-ddb-dev`). Idempotente. Registra `/configuracion/anfitriones` en el módulo `CONFIGURATION`. No toca allowances ni código Go/TS.
- No se aplicó el punto 3.2 del plan (`:opciones` en MEET_AGENDA): requiere decisión del usuario.
- No se hizo commit ni push. No se tocó `pa-prd`.

## Comandos y resultados
1. `aws sts get-caller-identity --profile pa-dev --region us-east-1`: Account 382670112717 (dev). OK.
2. `bash -n scripts/configure-meeting-hosts-iam-dev.sh`: sintaxis OK.
3. Estado previo (get-item, sin Scan) de `ind-dev-usuarios-ddb-dev-pri-use1` / `...#LV2#CONFIGURATION`: endpoints = `/configuracion/tributaria`, `/configuracion/acceso-maestro`.
4. Ejecución 1 del script: "Anfitriones agregado al módulo CONFIGURATION". Después: endpoints incluye `/configuracion/anfitriones`.
5. Ejecución 2 (idempotencia): "ya está registrado"; la lista queda con 3 elementos, sin duplicados.
6. No se desplegó nada (authorizer, api-gateway y services ya estaban desplegados). No hay cambios Go/TS, por lo que no aplican `go build/test` ni `tsc`.

## Pendiente de verificación (requiere al usuario)
- Cerrar sesión y volver a iniciar sesión en `nuevo.admin.dev.girasindomito.cl` (el claim `access` va dentro del JWT, 8 h).
- Luego confirmar en CloudWatch: `/aws/apigateway/indomito-hub-api-dev` con status 200 para `GET /configuracion/anfitriones`, y `Allow` en `/aws/lambda/iam-authorizer-dev`.
- No se confirmó el `profileCode` del usuario (se asume ADMIN) ni se probó con un token real.
- prd no se tocó; su registro equivalente es una decisión aparte.

## Reversión
Quitar el elemento `/configuracion/anfitriones` de la lista `endpoints` del ítem `APP#INDOMITO_HUB` / `APP#INDOMITO_HUB#LV1#ADM#LV2#CONFIGURATION`.

## Archivos temporales
Ninguno creado (no se emitieron tokens ni se descargaron logs).
