# Costos operacionales de Inbox

Los límites son controles de seguridad y costo, no supuestos de gratuidad.

- DynamoDB: escrituras transaccionales, Streams, índices y lecturas consistentes.
- SQS/DLQ: requests, retención de 4/14 días y redrives.
- API Gateway WebSocket: conexiones, minutos conectados y mensajes.
- Lambda: invocaciones, duración y concurrencia de endpoints/workers.
- S3: almacenamiento versionado, requests y transferencia de adjuntos/assets.
- Proveedores: WhatsApp Cloud API, Gemini, Transcribe y Google Calendar.

Antes del cutover se requieren alarmas de capacidad de conversaciones al 80 %, 90 % y 100 %, DLQ visible, errores Lambda, throttling y gasto por proveedor. El máximo materializable debe ser al menos `max(20000, ceil(activeCount*1.25))` y provenir del conteo/load test aprobado.
