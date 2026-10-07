# Cobranza contractual y tesorería

Estado: implementación en curso. No habilitar cobros reales hasta cerrar las pruebas de aceptación.

## Repos involucrados

| Repositorio | Responsabilidad |
| --- | --- |
| services | Contratos, dominio financiero, API, persistencia, comprobantes |
| app-ngx-hub | Puesta en marcha, anexos, cobranza, devoluciones y operación administrativa |
| app-ngx-pay | Consulta y pago público de cuotas, retorno Khipu y verificación de comprobantes |
| authorizer | Sesiones públicas acotadas y permisos administrativos |
| infrastructure | DynamoDB, S3 privado, trabajadores, configuración SSM |
| orchestrator | Contratos de integración y guía operativa |

## Requisitos aceptados

1. Aprobar un contrato genera una instantánea identificable e inmutable para pagos. Guardar un borrador no genera deuda. El formulario de contrato fija mes, año y día de inicio de cuotas; la puesta en marcha deriva los vencimientos de ese acuerdo, identifica a los liberados y valida su cantidad. Nunca infiere liberados por posición ni convierte el abono pactado en dinero recibido. Si un mes posterior carece del día pactado, vence el último día de ese mes y luego se retoma el día original.
2. Cada participación pertenece a un viaje y tiene identidad propia. RUT, pasajero, pagador y correo del comprobante son conceptos distintos. Los abonos efectivamente recibidos se distribuyen equitativamente, con resto CLP determinista y trazabilidad del ingreso original.
3. Altas, bajas, reemplazos, fechas y servicios se modifican por anexos aprobados, sin sobrescribir el contrato. Las cuotas de quienes continúan NO cambian automáticamente. El reemplazante no hereda pagos. La brecha económica permanece visible hasta resolverla.
4. Los descuentos extraordinarios conservan valor original, beneficiarios, base, porcentaje, monto, motivo privado y aprobador. La base por defecto es el saldo impago de las cuotas seleccionadas. No modificar obligaciones con intentos de pago no resueltos.
5. El cliente paga una cuota completa por operación y en orden. No se permiten pagos parciales. Para adelantar repite el proceso con la siguiente cuota. Un bloqueo condicional impide cobros concurrentes; un timeout no prueba que el banco no cobró.
6. Todo ingreso real se registra exactamente una vez, incluso si llega tarde, duplicado, con otro monto o tras una baja. Aplicaciones, excesos y devoluciones se distinguen. Webhook autenticado más consulta autoritativa a Khipu; nunca confiar en la redirección del navegador.
7. Pagos manuales exigen evidencia, referencia única, fecha efectiva y actor. Compiten por el mismo bloqueo que Khipu. El portal refleja la cuota pagada después de confirmar el registro.
8. En una baja, el abono inicial recibido no integra la base reembolsable. Un porcentaje manual de 0 a 100 sobre las cuotas efectivamente pagadas determina la devolución aprobada. Guardar base y redondeo. Aprobar una devolución crea una obligación; solo una salida confirmada reduce caja. Admitir devoluciones parciales, fallos y correcciones auditadas. No aplicar retención de baja a un pago duplicado por error.
9. Acceso público con RUT y código criptográficamente aleatorio de seis caracteres por viaje, generado antes del PDF aprobado, con reserva única y rotación. No solicitar OTP. Respuesta genérica, limitación por IP/viaje/RUT protegido y sin exponer nómina, motivos privados ni datos bancarios. Este acceso no acredita identidad del pasajero.
10. Correo válido obligatorio antes de pagar. Comprobante (no DTE) privado en S3 con ID estable y huella; descarga temporal y reenvío limitado a otro correo. Generación y correo SMTP mediante outbox durable. Un fallo de correo no deshace el pago.
11. Fechas de salida y regreso opcionales al crear/aprobar. Si faltan, PDF indica «fecha por definir». Un rango parcial o invertido es inválido. Duración y fecha de firma siguen siendo obligatorias. Sin salida no se calcula un plazo ficticio de pago anterior al viaje; las cuotas conservan sus vencimientos.
12. Tesorería distingue cobranza, compromisos, ingresos, conciliación bancaria, fondos pendientes, comisiones, proveedores, devoluciones pendientes y efectivas. Aperturas y reversos auditables. Vistas por grupo y consolidadas, alertas de vencimiento y grupos sin fecha.
13. Enteros CLP, sumas exactas y reparto determinista sin multiplicar redondeos al alza. Diario financiero versionado e inmutable, idempotencia durable y saldos verificables.
14. DynamoDB sin Scan y sin GSI inicial obligatorio. Lecturas por clave y relaciones pequeñas; sin historiales embebidos ilimitados. Operaciones masivas por lotes versionados y publicación solo al completar; respetar 100 acciones/4 MB por transacción.
15. Eventos versionados para BI y límites de integración futura con DTE/SII. No implementar ni declarar certificación tributaria o contabilidad legal completa en esta etapa.
16. Todo `PAYMENT_RECEIVED` confirmado desde Khipu crea exactamente una solicitud tributaria idempotente. En esta etapa solo se gestiona la emisión manual de boleta de ventas electrónica: un operador la emite en el SII, registra sus datos y adjunta el PDF privado. El pago, el comprobante interno y la boleta siguen siendo objetos independientes.
17. El navegador nunca propone ni modifica el monto tributario: la solicitud lo deriva del evento financiero confirmado. Registrar una emisión manual exige folio, fecha, PDF válido y actor; no equivale a una aceptación automática del SII. Reversas y devoluciones conservan el historial y abren revisión, nunca borran la emisión.
18. La misma solicitud versionada debe permitir que un futuro facturador certificado procese automáticamente boletas y facturas sin permisos para modificar cuotas, caja, comprobantes ni eventos financieros. Los datos tributarios personales permanecen fuera de BI.
19. Aprobar un contrato conserva el PDF definitivo generado por el sistema y abre una obligación documental independiente: cargar posteriormente una copia PDF firmada presencialmente por todas las partes. La carga es obligatoria, pero no tiene fecha máxima ni estado de atraso y su ausencia no desaprueba el contrato ni bloquea pagos o puesta en marcha.
20. El PDF firmado nunca reemplaza ni modifica el PDF aprobado. Cada carga publicada es inmutable, queda vinculada a la versión y huella del contrato aprobado, registra huella, tamaño, actor y fecha, y permite reemplazo solo mediante una versión nueva con motivo auditado. Solo administración puede cargar, consultar o descargar estos documentos.
21. Los frontends administrativo y público son aplicaciones Angular independientes. `app-ngx-hub` no compila, empaqueta ni despliega rutas o servicios del portal público; `app-ngx-pay` no contiene autenticación, IAM, menús, cobranza, tesorería ni otra feature administrativa. Ambos conservan Angular 22, PrimeNG 22, TailwindCSS 4, el mismo lenguaje visual y el mismo backend, pero tienen repositorio, configuración, pruebas, artefacto, bucket/CDN y ciclo de despliegue propios. La separación no modifica contratos HTTP ni obliga a cambios de backend.

## Hallazgos que condicionan la migración

- Las cláusulas actuales 18 y 21 no expresan las reglas nuevas de cuotas y devolución. Versionar la política; no modificar PDFs aprobados históricos ni aplicar automáticamente la política nueva a contratos antiguos.
- Los contratos históricos pueden guardar un mes sin año ni día. Los nuevos exigen mes, año y día en Crear contrato. No inferir fechas de históricos; cualquier regularización requiere acuerdo explícito.
- Los plazos de las cláusulas 12 y 17 pueden diferir. La puesta en marcha debe resolver una fecha límite explícita; no escoger silenciosamente una.
- El portal desplegado inicialmente era un simulador y no demuestra cobranza real integrada.

## Aceptación crítica

Dos cobros simultáneos; reintento tras timeout de creación; webhook duplicado/fuera de orden; pago tardío tras baja; descuento con intento abierto; pago manual concurrente; devolución parcial/repetida; cuota completamente descontada; reparto con resto CLP; liberados sin identificar; contrato sin fechas; grupo grande; correo/S3 fallidos; consulta de otro pasajero; rotación del código; aislamiento DEV/PRD; saldos del diario iguales a las proyecciones; contrato histórico aprobado sin firmado; carga firmada inválida; dos cargas firmadas concurrentes; reintento después de perder la respuesta; reemplazo con historial y autorización cruzada.
