# Conciliación de intentos Khipu

## Estados

- `pending` o `verifying`: conserva el bloqueo.
- rechazo o abuso antes del vencimiento: conserva el bloqueo, porque el pagador aún puede retomar el pago.
- `pending` no pagado con `expires_date` vencida: libera el intento sin asiento de ingreso.
- `done/normal`: confirma el monto real; una diferencia queda en fondos no aplicados.
- `marked-paid-by-receiver`: revisión manual; no demuestra transferencia bancaria.
- `reversed`: revierte el efecto del ingreso confirmado. Si fondos no aplicados ya se comprometieron o devolvieron, se rechaza la automatización para revisión humana.

La URL de cancelación y el retorno del navegador nunca resuelven el estado. La consulta administrativa usa el ID guardado; si la creación no alcanzó a persistir respuesta, exige copiar el `payment_id` desde Khipu y valida que su `transaction_id` corresponda al intento.

Fuentes oficiales revisadas el 2026-10-05:

- https://docs.khipu.com/openapi/en/v1/instant-payment/openapi/operation/getPaymentById/
- https://docs.khipu.com/es/payment-solutions/instant-payments/description
