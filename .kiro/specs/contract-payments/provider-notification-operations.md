# Notificaciones durables de pagos Khipu

Estado al 2026-10-04: implementación local completa; no desplegada ni validada contra DynamoDB Streams/Khipu reales.

## Flujo

1. El webhook valida método, tamaño, firma y antigüedad sobre el cuerpo original.
2. Extrae únicamente `payment_id` y `transaction_id` válidos.
3. En una transacción guarda `PROVIDER_NOTIFICATION#khipu#<paymentId>/META` y `PROVIDER_JOB#<fecha UTC>/PENDING#<paymentId>`.
4. Solo después responde `received=true`. Esa respuesta significa **notificación recibida**, no pago confirmado.
5. DynamoDB Streams invoca el trabajador con lotes de un registro y filtro `PROVIDER_JOB#*` + `PENDING`.
6. El trabajador consulta Khipu autoritativamente y usa la confirmación financiera idempotente existente.
7. Tras confirmar el ingreso y su comprobante, cierra inbox y job como `PROCESSED`/`DONE` mediante una transacción con versiones esperadas.

Si la gira está temporalmente bloqueada por un anexo, Khipu no responde o una escritura falla, el registro queda PENDING. El stream reintenta hasta cinco veces dentro de una hora. Después de agotar los reintentos, la señal sigue persistida y `ProcessProviderNotification` permite recuperación idempotente; falta exponer seguimiento/reintento administrativo dentro de KHIPU-04.

## Privacidad, costo e idempotencia

- No se guarda cuerpo, firma, correo, RUT, API key ni respuesta completa del proveedor en el inbox.
- Una notificación nueva agrega dos items pequeños y produce una invocación de stream. Un replay hace lecturas consistentes, sin escrituras nuevas.
- El worker usa batch 1 para aislar movimientos monetarios; esta elección aumenta invocaciones, pero reduce el alcance de un fallo y simplifica la idempotencia.
- No se agregó GSI, Scan, tarea programada ni cola permanente. La recuperación operativa usa la clave conocida del pago y el job fechado.
- Veinte trabajadores concurrentes aplican una sola confirmación, un solo diario y un solo comprobante.

## Validación pendiente del usuario

- Desplegar primero la tabla con stream existente y luego `api-payment`.
- Comprobar el filtro del event source mapping, IAM por función, reintentos y métricas en DEV.
- Simular plan bloqueado, desbloquearlo y comprobar que la cuota se aplique desde el trabajo PENDING.
- Agregar alarma/operación administrativa para trabajos agotados antes de habilitar checkout público.
