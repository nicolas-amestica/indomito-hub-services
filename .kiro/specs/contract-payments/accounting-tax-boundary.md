# Frontera contable y documentos tributarios

## Tres objetos distintos

| Objeto | Qué acredita | Fuente de verdad |
|---|---|---|
| Pago/ingreso | Movimiento financiero verificado y su aplicación o revisión | Diario de cobranza y caja |
| Comprobante interno | Constancia privada del registro de ese ingreso | Evento financiero + PDF inmutable S3 |
| DTE | Documento tributario emitido por un sistema certificado y aceptado según su ciclo externo | Futuro módulo tributario |

Un comprobante interno nunca cambia de categoría para convertirse en boleta o factura. El futuro emisor consume solicitudes versionadas posteriores al pago; no tiene permisos para cambiar cuentas, cuotas, caja ni comprobantes.

## Contrato futuro

`services/api-payment/contracts/tax-document-request-v2.schema.json` define la frontera operativa vigente. La v1 queda como contrato histórico de diseño y no se reescribe. `sourceEventId` y `requestId` hacen idempotente la emisión. El estado `ACCEPTED` solo podrá establecerse con una respuesta durable del proveedor/SII; una pantalla de retorno, un PDF cargado o un correo no bastan.

La primera implementación operativa consume únicamente pagos Khipu confirmados y crea una solicitud de `BOLETA_VENTA_ELECTRONICA` en modo `MANUAL_SII`. El operador emite fuera del sistema y registra folio, fecha y PDF. Ese registro queda como `MANUAL_RECORDED`: no se presenta como aceptación electrónica del SII. La automatización posterior podrá consumir la misma solicitud para boletas o facturas sin cambiar el evento financiero de origen.

La bandeja se proyecta por `TAX_QUEUE#PENDING` y `TAX_QUEUE#RECORDED`; se consulta por partición, sin `Scan` ni GSI. El registro manual reserva además el folio por clave, mueve la proyección y guarda el comando en una sola transacción. El monto no viaja desde el formulario: permanece congelado desde el evento financiero. El PDF se conserva cifrado en S3 privado, con nombre por SHA-256 y descarga administrativa temporal.

El envío posterior de una boleta cargada debe implementarse como una entrega durable separada (`TAX_DELIVERY` + outbox/trabajador), nunca como SMTP síncrono dentro del registro manual. Esto permitirá reintentos auditables y reutilizar el canal cuando exista un facturador certificado, sin convertir el correo en evidencia de aceptación del SII.

Los datos tributarios del receptor no se incluyen en eventos analíticos. Se obtendrán desde un repositorio privado, cifrado y con retención/acceso propios cuando se defina el tipo de documento y el responsable legal del dato.

## Reversas y devoluciones

- Un pago revertido no borra su solicitud ni el DTE histórico.
- Una devolución financiera se registra primero en cobranza/caja.
- Si corresponde un ajuste tributario, se crea una solicitud separada de nota de crédito o débito enlazada mediante `reversalOfRequestId`.
- Ningún porcentaje de devolución se transforma automáticamente en un documento tributario sin clasificación y revisión.

## Cierre y conciliación

El cierre mensual futuro debe producir cuatro totales reconciliables: ingresos bancarios, pagos aplicados/no aplicados, DTE aceptados y devoluciones/ajustes. Las diferencias quedan en `REQUIRES_REVIEW`; jamás se compensan silenciosamente. Cerrar un período impide mutaciones retroactivas y obliga a asientos/eventos compensatorios en un período abierto.

## Trabajo futuro regulatorio

Quedan fuera de este desarrollo la selección del proveedor, certificación, folios/CAF, firma electrónica, libros, ambientes de certificación, pruebas exigidas y operación ante el SII. Antes de construirlos se debe validar el modelo tributario con contador/asesor y documentación oficial vigente.
