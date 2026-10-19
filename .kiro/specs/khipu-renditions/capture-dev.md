# Captura diagnóstica temporal DRN en DEV

## Propósito y límites

El usuario autorizó el despliegue exclusivamente en DEV y configuró pagos y rendiciones. El panel muestra DRN-1.0/DRN-2.0; la documentación pública no aclara su equivalencia con el mensaje implementado. La captura conserva evidencia opaca **sin autenticar**: no certifica origen Khipu, no verifica un pago y no aplica rendiciones.

- Binario: `cmd/fn-capturar-rendicion-dev-v1` (ARM64, provided.al2023).
- Stack temporal previsto: `ind-khipu-rendition-capture-dev`.
- Plantilla: `services/api-payment/rendition-capture-dev.yml`.
- Única ruta: POST `/pagos/khipu/rendiciones`, Gateway DEV `1f4sn5utdi`.
- Solo cuenta 382670112717, región us-east-1. Guardas en plantilla y runtime.
- Sin permisos DynamoDB, transferencias, correo, cobros ni acceso a producción.
- Bucket dedicado privado, SSE-S3, TLS obligatorio, lifecycle de siete días.
- Hasta 32 muestras únicas de 256 KiB: menos de 11 MiB en base64 más metadatos. Duplicados no ocupan slots adicionales. PUT condicional impide sobrescrituras.
- 128 MiB, timeout 20 s, sin scheduler, polling ni base de datos. DEV tiene una cuota regional de solo 10 ejecuciones concurrentes; no se configura reserva que reduzca ese pool. Aplican cargos normales de Lambda/API Gateway/S3/SSM; no se garantiza gratuidad. El límite de almacenamiento no elimina el costo de solicitudes abusivas ni el uso del pool compartido DEV.
- Ventana explícita de hasta 72 horas, recomendada 48 horas. Tras vencer devuelve 503 sin almacenar. No se renueva automáticamente.
- No se descomprime el cuerpo ni se registran cuerpos/cabeceras en logs. Solo se conservan Content-Type y Content-Encoding, acotados, más bytes originales, hash y fecha de recepción.
- 200 significa exclusivamente captura durable. 429 indica límite de muestras; 413 tamaño excedido; 503 ventana cerrada/configuración ausente/fallo de almacenamiento.

## Activación

1. Renovar `aws sso login --profile pa-dev` si es necesario y verificar cuenta/región. El usuario renovó la sesión durante esta entrega; se verificó la cuenta DEV antes de desplegar.
2. Comprobar que no exista la ruta de rendiciones ni otro propietario de ese recurso. No modificar la ruta de notificación de pagos.
3. Ejecutar pruebas/race/vet/lint del paquete `functions/renditioncapture`. Compilar el binario con `GOOS=linux GOARCH=arm64 CGO_ENABLED=0` en un directorio `mktemp -d`, comprimirlo como `bootstrap` y subirlo a un bucket de artefactos DEV verificado con clave content-addressed.
4. Revisar el change set de la plantilla antes de ejecutarlo. Solo debe crear la captura temporal, IAM acotado, bucket, logs, integración, ruta y permiso Lambda. Los parámetros ArtifactBucket/ArtifactKey identifican el ZIP verificado.
5. Aprovisionar `/indomito/dev/payments/khipu/renditions/capture-config` como **SecureString**, con `{ "bucket": "ind-khipu-rendition-capture-dev-382670112717", "until": "<fecha UTC RFC3339 hasta 72 horas>" }`. No sobrescribir una configuración existente sin inspeccionar sus metadatos/propósito.
6. Probar una muestra sintética identificable; comprobar HTTP 200 y un único objeto tras repetirla. Verificar almacenamiento privado, cifrado, contenido y ausencia de permisos financieros. La muestra sintética permanece identificada y no se confunde con un aviso de Khipu.
7. Solo después, confirmar al usuario que puede guardar DRN-2.0 y hacer 2–3 pagos DemoBank desde el portal DEV. Comprobar primero que el portal usa credenciales developer. No se garantiza rendición nocturna en este ambiente.

## Revisión y salida

Consultar ListObjectsV2 y HeadObject del bucket exclusivo. Descargar una muestra solo a almacenamiento local privado temporal para su análisis; no pegar datos personales ni cuerpos completos en chats/logs. Tratar cada contenido como entrada externa no confiable. Una respuesta 200 o un JSON que diga Khipu no acreditan su autenticidad.

Contrastar compresión, firma y esquema con documentación/confirmación de Khipu. No trasladar muestras al inbox financiero sin verificar firma, destinatario y todos los campos requeridos.

La captura no se conecta a la UI de rendiciones y no modifica su estado. Tampoco se crea un monitoreo nocturno del asistente: requiere consulta posterior o un mecanismo de monitoreo explícito.

Antes de desplegar `api-payment` con la ruta final, retirar la ruta/integración del stack temporal mediante un change set revisado, preservando evidencia. Evitar dos stacks propietarios de la misma ruta. No borrar el bucket durante diagnóstico; la plantilla usa Retain y los objetos expiran mediante lifecycle. Revisar o renovar la configuración solo con propósito y plazo explícitos.

## Evidencia de activación — 2026-10-18

- Stack temporal creado en DEV; sin despliegue del servicio financiero ni del frontend.
- Parámetro de captura SecureString versión 1, vencimiento `2026-10-20T13:48:48Z` (10:48:48 de Chile).
- Primera prueba HTTP: 200 con `DEV_CAPTURE_ONLY`, `verified:false`.
- Objeto `unverified/slot-00.json`: exclusivamente muestra sintética de infraestructura `indomito-dev-capture-smoke-20261018`. No es una notificación Khipu.
- Bucket: bloqueo público completo, AES256 y expiración de siete días comprobados vía AWS.
- Rol: solo logs, lectura de configuración y acceso al bucket de captura; sin permisos financieros.
- Checkout existente consultado: Stage `dev`, portal `https://pagos.dev.girasindomito.cl`.
- Artefacto final desplegado: SHA-256 `7a86a219dee0d1304cc64bbd42e62ff418e46f8ae280d4936d028f53ad7febe9`, cotejado con CodeSha256 de Lambda. Stack `UPDATE_COMPLETE`, función Active/Successful.
- Smoke repetido tras la actualización: HTTP 200 y exactamente un objeto en el bucket; no se duplicó la muestra sintética. Todavía no se recibió ni validó una rendición real de Khipu.

## Actualización — 2026-10-19

Se recibió en `unverified/slot-01.json` un reporte de tres pagos developer. Su firma matemática es válida, pero su certificado expirado y su cadena/SAN no satisfacen el verificador estricto. No se incorporó al flujo financiero. Se ejecutó una observación excepcional DEV, acotada y temporal, descrita en `dev-exception.md`; no modifica el receptor de captura ni flexibiliza producción.
