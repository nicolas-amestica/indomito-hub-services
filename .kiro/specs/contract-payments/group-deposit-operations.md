# Abono grupal recibido

## Regla financiera

- El abono pactado del contrato no crea un ingreso. Solo un movimiento verificado en cartola puede registrarse.
- La referencia bancaria se reclama una vez en todo el sistema.
- El monto se reparte entre pagantes activos con abono pendiente, en pesos enteros, orden estable y sin superar el pendiente individual.
- Si el monto excede el abono pendiente agregado, la operación se rechaza completa.

## Consistencia y recuperación

- El `commandId` público permanece en la URL administrativa. Un timeout se recupera consultando o reenviando exactamente la misma solicitud.
- La gira queda `APPLYING_GROUP_DEPOSIT` mientras se congelan y aplican asignaciones; otras mutaciones financieras que exigen plan activo se bloquean.
- Cada efecto por cuenta y la publicación final son idempotentes. El estado vuelve a `ACTIVE` únicamente después de completar todas las cuentas.
- El comprobante grupal se guarda en `receipts/groups/{tripId}/{receiptId}/v1.pdf`; se lista por la partición de la gira y su descarga valida la ruta exacta antes de firmarla por 120 segundos.

## Despliegue pendiente

No se desplegó. Antes de habilitarlo en DEV se deben desplegar API, worker de comprobantes, tabla/IAM/S3 relacionados y frontend; después ejecutar reconciliación con una cartola de prueba sin dinero real.
