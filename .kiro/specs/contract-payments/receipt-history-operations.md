# Comprobantes históricos e identidad

Los comprobantes nuevos usan documento v2 e incluyen una instantánea del nombre y RUT del pasajero obtenida desde la nómina vigente al registrar el ingreso. El correo y las referencias bancarias no aparecen en el PDF.

La migración histórica se ejecuta por cuenta, con páginas de diez comprobantes y simulación previa (`apply=false`). Solo acepta comprobantes financieros ya existentes y los cruza con la proyección `TRIP/MEMBER`; no crea pagos, eventos ni saldos.

Al aplicar, cada comprobante v1 se actualiza mediante una transacción condicional e idempotente y se encola en modo `DOCUMENT_ONLY`. Se crea `v2.pdf` sin reenviar correos antiguos; el objeto v1 permanece inmutable en S3. Una versión 2 existente nunca se sobrescribe.
