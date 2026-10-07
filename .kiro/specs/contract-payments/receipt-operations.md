# Comprobantes: implementación y preparación operativa

Estado: código local, sin deploy ni envíos SMTP/AWS realizados en esta etapa. El plan completo de pagos todavía no está terminado. No habilitar checkout visible hasta resolver los pendientes de `tasks.md`.

## Qué queda conectado

- El ingreso confirmado registra cuenta, evento, referencia única, metadatos de comprobante, enlace por cuenta y trabajo de emisión en la misma transacción.
- `services/payment-receipts` es un servicio Go que genera el PDF privado y envía mediante `go-mail`, TLS >=1.2 y SMTP 465/587. El remitente DEV permitido es `no-reply-dev@girasindomito.cl`. El mensaje y PDF se identifican como desarrollo y no como boleta/factura SII.
- El portal público no lista ni descarga comprobantes históricos. Las rutas antiguas `/pagos/portal/comprobantes` y `/{id}/descarga` fueron retiradas de Serverless y denegadas por el authorizer.
- POST `/pagos/portal/intentos/{attemptId}/comprobante/reenvios`: una copia a otro correo, solo desde la misma sesión que creó el checkout, después de `PAYMENT_RECEIVED` y cuando el PDF está listo. El servidor deriva el comprobante desde el intento; el navegador nunca envía su ID. Una marca transaccional impide un segundo reenvío público.
- Administración conserva `/pagos/cuentas/{accountId}/comprobantes`, descarga y POST `/{id}/reenvios`. Puede repetir reenvíos con nuevos comandos; cada entrega conserva actor, destinatario y huella.
- El frontend exige correo y conserva solicitud/destinatario al reintentar un error de respuesta. Informa solicitud registrada, no correo entregado. Los datos de sesión pública no se guardan en almacenamiento web.

## Persistencia y costo

| Clave | Uso |
| --- | --- |
| `ACCOUNT#id / RECEIPT#id` | Listado acotado por cuenta; 20 registros por página |
| `RECEIPT#id / META` | Evento original, correo inicial, documento, hash y estado de entrega |
| `RECEIPT#id / DELIVERY#id` | Reenvío independiente, destinatario, actor, huella y estado propios |
| `RECEIPT#id / PORTAL_RESEND#attemptId` | Uso único del reenvío público ligado al intento y sesión originales |
| `JOB#yyyy-mm-dd / PENDING#id` | Trabajo recuperable por fecha UTC de registro; status pasa a SENT o DELIVERY_FAILED |

Los identificadores de comprobantes derivados de comandos pueden ser deterministas: el orden de la lista NO representa orden cronológico. La fecha efectiva se muestra por separado.

El reenvío administrativo hace dos escrituras transaccionales (entrega y job); el público agrega la marca de uso único. No añade un ingreso, evento financiero ni GSI. El historial es exclusivamente administrativo y usa Query. No se promete costo cero ni cobertura total por Free Tier: tabla on-demand, S3/versiones, Lambda/logs, SQS y SMTP deben medirse.

TTL solo en la marca pública efímera; la sesión de diez minutos impide reutilizarla desde una consulta posterior. Ni el comprobante, ni las entregas auditables, ni el diario financiero expiran automáticamente.

## Seguridad e invariantes

- Bucket independiente, bloqueo público, AES256, versiones, HTTPS obligatorio y Retain ante eliminación/reemplazo del stack. Retain no reemplaza una estrategia de respaldo ni impide que un administrador borre objetos manualmente.
- PDF bajo `receipts/{accountId}/{receiptId}/v1.pdf`, `If-None-Match: *` y SHA-256. Un conflicto de bytes nunca sobrescribe el original.
- Descarga administrativa de 120 segundos después de comprobar cuenta y clave/hash exactos. El portal público nunca obtiene esta URL.
- Worker sin endpoints públicos, sin permisos para escribir ACCOUNT/EVENT/REFERENCE ni secretos Khipu; cambios limitados a RECEIPT/JOB y documentos.
- El correo usa adjunto binario y bloquea acceso a archivos/URL indicado por contenido. No se incluyen correos, RUT ni referencias bancarias en el PDF.
- Aceptación SMTP no demuestra entrega en bandeja. Una respuesta SMTP perdida puede producir correo duplicado: el transporte es al menos una vez. La identidad del PDF y el ingreso financiero permanecen únicos.
- Cada entrega tiene lease de 120 segundos y hasta cinco intentos; Lambda tiene timeout de 60 segundos. Las fallas terminales cierran el job como DELIVERY_FAILED y conservan el fallo del stream para que alcance la cola SQS declarada. Se almacena únicamente un código permitido (`lastFailureCode`), sin mensajes SMTP crudos. No liberar leases ni reiniciar contadores con ediciones manuales improvisadas.
- El reenvío conserva estado y destinatario originales. El nuevo destinatario no obtiene identidad administrativa ni cambia el contacto del pasajero.

## Preparación para el deploy del usuario

1. Revisar y probar cambios de los cuatro repositorios; no ejecutar deploy masivo del workspace.
2. En `infra/environments/.env.dev`, cargar la variable opcional `SSM_PAYMENTS_RECEIPT_SMTP`, procesada por el mecanismo SecureString existente. Valor JSON con `host`, `port` (465 o 587), `user`, `password` y `from`. No guardar credenciales en Git, comandos compartidos ni documentación. El worker lee `/indomito/dev/payments/receipt-smtp` con desencriptado y caché de cinco minutos.
3. El JWT público sigue requiriendo `SSM_AUTH_PAYMENT_SESSION_SECRET`, distinto de claves Khipu y del JWT administrativo. No reutilizar claves.
4. Orden de dependencias: SSM; DDB con stream NEW_IMAGE; S3 con outputs ReceiptsBucketName/Arn; authorizer con rutas públicas explícitas; api-payment; worker `payment-receipts`; frontend. Confirmar que el stream esté activo antes de configurar el worker. No cambiar el Gateway o CDN si no existe otro cambio pendiente específico.
5. El worker Go es un stack independiente: ejecutar `go test -race ./...`, `go vet ./...` y desde la raíz `make build/validate/deploy service=services/payment-receipts`. Los comandos de despliegue resuelven referencias AWS y quedan a cargo del usuario.
6. Dar a personal autorizado el módulo `PAYMENT_OPERATIONS` y permisos por segmento `/pagos/cuentas`: `r` lectura/descarga, `c` operaciones/reenvío. No otorgar `/pagos` completo ni permisos administrativos a sesiones públicas.
7. Revisar SQS de fallos, permisos de stream/S3/SSM y el proveedor SMTP. Probar primero con un destinatario controlado, una cuota de DemoBank y descarga autenticada. Verificar un reenvío y repetir la misma solicitud para comprobar que no genere otra entrega.
8. Mantener `checkoutEnabled` desactivado hasta la aprobación operativa del recorrido completo. Este documento no autoriza cargos reales.

## Recuperación acotada

Comando: `go run ./cmd/recover-receipts --date AAAA-MM-DD` desde `services/payment-receipts`. Simula por defecto; `--apply` puede generar documento y enviar correo. No se ejecuta en builds ni automáticamente al desplegar.

Desde la carpeta del worker, con perfil `pa-dev`, región `us-east-1`, `APP_STAGE=dev` y nombre real de tabla en `PAYMENTS_TABLE_NAME`:

```bash
AWS_PROFILE=pa-dev APP_STAGE=dev PAYMENTS_TABLE_NAME=ind-dev-pagos-ddb-dev-pri-use1 \
  go run ./cmd/recover-receipts --date 2026-10-01 --limit 20
```

Simula por defecto: solo Query y salida mínima sin correos. Si retorna `nextCursor`, pedir explícitamente la siguiente página con `--cursor ID`. La fecha es la de registro del job en UTC, no la fecha efectiva bancaria.

Agregar `--apply` procesa esa página y **puede enviar correos reales**; requiere además bucket y SMTP configurados. Revisar la simulación antes de ejecutarlo. Una entrega SENT no se vuelve a enviar, aunque su job quedó pendiente por respuesta perdida. DELIVERY_FAILED no se reactiva: revisar el motivo y usar una nueva solicitud autorizada de reenvío cuando corresponda. Un resultado RETRY_FAILED no borra ni modifica los fondos.

Para el operador: GetItem/UpdateItem en RECEIPT, Query/UpdateItem en JOB; para aplicar, también PutObject/GetObject del prefijo receipts y GetParameter del SMTP. No necesita permisos sobre cuentas financieras. No otorgar Scan.

## Históricos y límites pendientes

- Registros existentes sin `ACCOUNT#/RECEIPT#` requieren backfill explícito por fecha/cuenta conocida; esta versión no los encuentra mediante exploración global.
- Jobs anteriores a la retención del stream requieren la recuperación por fecha; TRIM_HORIZON no recupera registros de tabla antiguos sin evento de stream disponible.
- Confirmaciones históricas sin `Event.AttemptID` no deben reinterpretarse automáticamente para satisfacer la nueva validación de replay. Preparar revisión/migración con evidencia.
- El PDF identifica viaje/cuenta mediante IDs técnicos. Mejorar la identificación legible con una instantánea autorizada de datos contractuales es pendiente, sin exponer nombres/RUT en la respuesta pública de búsqueda.
- Faltan dashboard de fallos de entrega, medición real de costos, pruebas reales de permisos y recorrido AWS/SMTP. El hash es control de integridad, no firma tributaria ni certificación SII.

## Evidencia local

381 pruebas frontend; builds administrativo y portal DEV; trabajador Go con race/vet; api-payment con race/vet/lint; comprobación del esquema serializado en DynamoDB; pruebas de authorizer y almacenamiento. El portal se probó con respuestas API ficticias en 320, 390, 768 y 1440 px, temas claro/oscuro. No hubo llamadas reales a Khipu, AWS ni SMTP durante esas pruebas.
