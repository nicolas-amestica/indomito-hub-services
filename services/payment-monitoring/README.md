# Supervisión de rendiciones Khipu

Servicio Go independiente de `api-payment`, sin endpoints públicos ni permisos
para modificar movimientos financieros. SMTP usa `github.com/wneessen/go-mail` y el SecureString
`/indomito/{stage}/payments/receipt-smtp`; nunca SES ni certificados TLS permisivos.

## Configuración y alcance

Registrar mediante infrastructure, `environments/.env.prd`:

```dotenv
SSM_PAYMENTS_KHIPU_RENDITIONS_MONITORING_CONFIG={"enabled":true,"email":"khipu@work.girasindomito.cl","startMonth":"2026-10","pendingMinutes":30,"unsettledHours":72,"reminderHours":24,"maxPages":20}
```

Los 30 minutos y 72 horas son umbrales operativos configurables, **no un SLA de
Khipu**. Las horas son corridas, incluyendo fines de semana. `startMonth` debe ser
el primer mes con pagos productivos. No se revisan movimientos anteriores a ese mes.
No sustituye la comparación independiente con cartola.

Cada 15 minutos consulta por `Query` los índices mensuales `RENDITION_MONTH#` y
`CASH#`, y confirma el estado actual mediante `GetItem`. No usa Scan. Prioriza el
mes actual y mantiene otro cursor persistente para volver a revisar todo el
histórico desde `startMonth`, sin descartar pendientes por antigüedad. Máximo 20
páginas de 50 registros por ejecución. Si el histórico crece, revisar el avance de
`RENDITION_MONITOR/CURSOR#HISTORY` y ajustar `maxPages` o frecuencia; el barrido
completo no tiene latencia fija. Páginas fallidas se repiten sin avanzar el cursor.

Detecta OBSERVED, REVIEW_REQUIRED, PENDING demorado y pagos Khipu confirmados sin
liquidación completa. Los reportes reprocesados cuentan la demora desde
`PendingSince`; registros antiguos sin ese campo usan `ReceivedAt`. No alerta por
ausencia de ventas. Cambios no admitidos de comisiones/destino/firma siguen
rechazándose o pasando a revisión: nunca se relajan las verificaciones de PRD.

Correos agrupados por página (hasta 50 casos), con recordatorio por caso cada 24
horas. Solo persiste el envío después de aceptación SMTP. Un fallo posterior a la
aceptación SMTP puede repetir un aviso: entrega al menos una vez, no exactamente
una vez. Esto nunca repite una liquidación. No incluye cuentas bancarias,
certificados, credenciales ni datos de pasajeros.

## Alarmas técnicas

CloudWatch → SNS privado → Lambda Go `alarm` → SMTP. Notifica ALARM y OK por:
errores/throttling del receptor y trabajador, rechazos HTTP del receptor, falta de
ejecuciones del trabajador, fallos o falta de heartbeat del monitor. El emisor
normal publica `ReceiverRejected` incluso cuando retorna HTTP 400/503 sin error
de ejecución Lambda. Requiere desplegar también `api-payment`.

El envío técnico tiene reintentos Lambda y cola de fallos por 14 días. Las alarmas
`EmailDeliveryErrors` y `EmailDeadLetters` quedan visibles en CloudWatch sin enviar
a sí mismas para evitar un bucle. **Si SMTP o el buzón falla, este mismo canal no
puede garantizar avisarte**: revisar estas alarmas en AWS o agregar otro canal
independiente en el futuro. No es necesario confirmar una suscripción SNS de
correo, porque SNS invoca una Lambda; sí hay que comprobar recepción real en el
buzón. Se crean diez alarmas, un topic, una cola y dos Lambdas; el costo depende
de uso y de la cuota gratuita disponible, no se garantiza costo cero.

## Despliegue manual (no ejecutado por el agente)

1. Infraestructura, desde su raíz:
   `make deploy resource=ssm stage=prd region=us-east-1`.
2. API, desde la raíz del repositorio backend:
   `make deploy service=services/api-payment stage=prd region=us-east-1`.
3. Servicio de monitoreo, desde la raíz del backend, usando el builder Go estándar:

   ```bash
   (
     unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_SECURITY_TOKEN
     export AWS_PROFILE=pa-prd
     make deploy service=services/payment-monitoring stage=prd region=us-east-1
   )
   ```

4. Frontend administrativo, desde su repositorio:
  `npm run build:prd` y `npm run deploy:prd`.

### Actualización de Node a Go sobre el servicio ya desplegado

Si los cuatro componentes anteriores ya están desplegados, **solo se redespliega
`payment-monitoring`** con el comando del paso 3. No eliminar el stack ni cambiar
sus nombres: se conservan `monitor`, `alarm`, sus IDs lógicos, las diez alarmas,
SNS, DLQ, regla cada 15 minutos, IAM y variables SSM. Solo cambia el runtime a
`provided.al2023` y cada función recibe su ZIP ARM64 con `bootstrap`.

Los cursores `CURSOR#CURRENT/HISTORY`, `after`, recordatorios SHA-256 `ALERT#` y
concesión `LEASE` mantienen el formato DynamoDB de Node, incluyendo milisegundos
en `until`. No borrar esos registros: permiten continuar sin reiniciar el histórico
ni los recordatorios. Se validan con pruebas de compatibilidad. Verificar después
del despliegue el heartbeat y un correo de prueba; empaquetar no verifica entrega.

No cambiar la URL Khipu ni la política APPLY por estos cambios. No volver a
reprocesar la rendición ya completada 78779299. El botón se ofrece únicamente
para OBSERVED/REVIEW_REQUIRED y usuarios con permiso de creación en
PAYMENT_OPERATIONS; el authorizer vuelve a validar el POST en servidor.

## Aceptación posterior al despliegue

- Verificar stack, reglas habilitadas y `MonitorHeartbeat` dentro de una hora.
- Consultar `RENDITION_MONITOR/CURSOR#CURRENT` y `CURSOR#HISTORY` (GetItem), sin
  modificar datos de pagos. Los cursores deben progresar durante un barrido.
- Hacer una prueba explícita del canal: poner manualmente `ReceiverErrors` en
  ALARM mediante la consola CloudWatch (estado de prueba, no inducir un fallo
  real), confirmar recepción y posterior OK. Esta prueba no verifica firmas ni
  debe realizar pagos nuevos. Requiere autorización del operador.
- Verificar una excepción real en la pantalla: abrir/cancelar confirmación;
  reprocesar únicamente si se corrigió la causa y se desea aplicar la política.
- Tras un reporte normal, confirmar COMPLETED, ausencia de diferencias y que no
  exista liquidación duplicada. El webhook nuevo no depende de Postman.

## Pruebas locales

```bash
go test -race ./...
go vet ./...
golangci-lint run ./...
../../node_modules/.bin/tsx --test test/serverless.test.ts
```

Referencias: [CloudWatch y datos ausentes](https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/alarms-and-missing-data.html),
[acciones de alarmas](https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/alarm-actions.html),
[SMTP, TLS y contexto en go-mail](https://github.com/wneessen/go-mail).
