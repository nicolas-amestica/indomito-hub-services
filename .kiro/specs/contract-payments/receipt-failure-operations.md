# Entregas fallidas de comprobantes

## Consulta

La vista administrativa consulta una sola partición `JOB#AAAA-MM-DD`, con páginas de hasta 20 trabajos. No usa `Scan` ni GSI. Solo muestra entregas agotadas y códigos de error controlados; no expone direcciones de correo, contenido SMTP ni datos personales.

## Reintento

Antes de reintentar, el operador debe corregir o verificar la causa externa y registrar un motivo. La operación:

- exige que la entrega siga en `DELIVERY_FAILED`;
- reinicia únicamente el contador y estado de entrega;
- conserva el evento, monto, saldo y PDF inmutable;
- crea un trabajo diario y un comando idempotente en la misma transacción;
- permite recuperar una respuesta perdida sin encolar dos veces.

SMTP tiene semántica de entrega al menos una vez: una caída después de que el servidor aceptó el mensaje podría producir una copia adicional. Esto nunca debe interpretarse como un segundo pago.
