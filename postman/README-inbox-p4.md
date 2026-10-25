# API Inbox P4 E2E

La colección `api-inbox-p4-e2e` prepara pruebas destructivas contra DEV. No debe
ejecutarse en PRD. Su validación estructural local no demuestra comportamiento real.

## Preparación segura

1. Copiar `postman/environments/inbox-dev.environment.yaml` como
   `inbox-dev.local.environment.yaml` y asignarle permisos `0600`.
2. Completar solamente en la copia local `base_url`, `access_token`, `phone` y
   `reaction_message_id`, marcándolos `enabled: true`.
3. Copiar `postman/data/inbox-p4-media.example.json` como
   `inbox-p4-media.local.json` y completar rutas absolutas, tamaños y SHA-256 en
   Base64. Ambos patrones locales están ignorados por Git.
4. Usar exclusivamente un número DEV autorizado y archivos sintéticos sin datos
   personales.

Para obtener tamaño y checksum en macOS:

```bash
stat -f '%z' /ruta/archivo
openssl dgst -sha256 -binary /ruta/archivo | openssl base64 -A
```

El dataset debe contener 13 filas: los 11 MIME habilitados y stickers WebP estático
y animado. `attachment_kind_property` queda vacío para adjuntos normales y vale
`,"kind":"sticker"` para stickers.

## Validación estructural

```bash
postman collection lint postman/collections/api-inbox-p4-e2e
postman environment lint postman/environments/inbox-dev.environment.yaml --fail-severity warning
```

Exigir explícitamente `Errors: 0`; Postman CLI 1.76 puede devolver exit code 0 aun
cuando el texto del lint contiene errores de formato.

## Validación local del arnés

El mock `postman/mocks/api-inbox-p4` reproduce únicamente los contratos necesarios
para ejecutar la colección y encadena la reserva multimedia con su confirmación. No
simula Meta, S3, DynamoDB, SQS ni WebSocket y no prueba el backend real.

```bash
make test-inbox-p4-harness
```

El comando levanta y detiene automáticamente un mock aislado para cada bloque, falla
ante el primer error y no usa secretos ni servicios externos. Ejecuta el bloque JSON
y el bloque multimedia con `postman/data/inbox-p4-media.mock.json`. Este dataset usa
un fixture sintético único con nombres/MIME distintos; valida las 13 iteraciones del
runner, no la decodificación real de esos formatos.

La colección separada `api-inbox-p4-local-resilience` nunca debe apuntarse a DEV o
PRD. Comprueba autenticación, JSON malformado, MIME, tamaño, checksum, expiración,
deduplicación, secuencia de reintentos y error permanente. La referencia local deja
8 requests/16 aserciones para JSON, 39 requests/65 aserciones para multimedia y 19
requests/35 aserciones de resiliencia, todas aprobadas. La secuencia stateful prueba
el contrato del runner y del mock; las pruebas Go prueban los reintentos del backend.

## Ejecución autorizada

Primero ejecutar las capacidades JSON una vez:

```bash
postman collection run postman/collections/api-inbox-p4-e2e \
  -e postman/environments/inbox-dev.local.environment.yaml \
  -i 'Listar conversaciones' \
  -i 'Enviar texto' \
  -i 'Enviar plantilla aprobada' \
  -i 'Enviar ubicación' \
  -i 'Enviar contacto' \
  -i 'Enviar mensaje interactivo' \
  -i 'Enviar reacción' \
  -i 'Verificar conversación' \
  --delay-request 500 \
  --bail \
  --no-report-events \
  --output "$INBOX_P4_PRIVATE_DIR/inbox-p4-json"
```

Después ejecutar solo la secuencia multimedia, una iteración por fila:

```bash
postman collection run postman/collections/api-inbox-p4-e2e \
  -e postman/environments/inbox-dev.local.environment.yaml \
  -d postman/data/inbox-p4-media.local.json \
  -i 'Preparar multimedia' \
  -i 'Subir multimedia a URL firmada' \
  -i 'Confirmar envío multimedia' \
  --delay-request 500 \
  --bail \
  --no-report-events \
  --working-dir / \
  --output "$INBOX_P4_PRIVATE_DIR/inbox-p4-media"
```

El primer run realiza 8 requests. El segundo realiza 39 requests y cubre 13 archivos.
Los estados `200` y `202` prueban aceptación durable, no entrega final. Para cerrar
P4 se debe correlacionar cada `clientMessageId` con WAMID, webhook de estado,
persistencia y evento realtime, y adjuntar el reporte del runner.

Antes de ejecutar, crear `INBOX_P4_PRIVATE_DIR` fuera del repositorio con `umask 077`
y permisos `0700`. Los reportes crudos son transitorios: pueden incluir URLs y datos
de prueba, por lo que no deben adjuntarse ni versionarse.

Para producir evidencia correlacionada y sanitizada, copiar y completar únicamente
los identificadores técnicos de `postman/data/inbox-p4-evidence.example.json` y usar:

```bash
node scripts/collect-api-inbox-p4-evidence.mjs entrada-privada.json evidencia-sanitizada.json
```

El recolector rechaza campos con teléfonos, tokens, secretos, payloads o cuerpos,
elimina los IDs originales y escribe la salida con permisos `0600`. Al terminar,
mover a la papelera el directorio privado y las copias locales con secretos.

## Readiness local completo

Con Node `v24.15.0`, el siguiente comando ejecuta contratos, recolector, Postman,
Go con race detector, lint/build y las pruebas/build del Inbox Angular:

```bash
make test-inbox-local-readiness
```

Este gate no realiza despliegues ni llamadas a DEV, PRD, Meta o AWS.
