# Preparación para Inteligencia de Negocio

BI no debe consultar ni escanear las tablas DynamoDB operacionales. La frontera será un evento analítico v1 derivado después del commit financiero y publicado mediante outbox; nunca participa en la transacción bancaria ni puede modificar cobranza.

El contrato canónico está en `services/api-payment/contracts/analytics-event.schema.json`. Usa CLP entero, fecha efectiva, gira, cuenta opcional y una referencia fuente idempotente. No exporta RUT, nombre, correo, código de viaje, token, motivo interno ni referencia bancaria completa.

## Canal futuro

1. Un proyector lee eventos confirmados desde DynamoDB Streams.
2. Convierte únicamente tipos permitidos al esquema versionado.
3. Escribe archivos Parquet particionados por `effectiveYear/effectiveMonth`, cifrados en S3.
4. Mantiene un checkpoint idempotente por `sourceReference`.
5. Athena/Glue o la herramienta BI consulta esas particiones, nunca la tabla productiva.

Reprocesar un período debe producir el mismo conjunto lógico. Reversas se publican como eventos compensatorios con `reversalOf`; no se corrigen filas históricas. La suma analítica por gira/fecha debe conciliar con las proyecciones de caja antes de publicar un dataset.

La infraestructura de exportación, catálogo y dashboards no se crea todavía: requiere definir retención, frecuencia, usuarios, herramienta BI y presupuesto. El esquema deja esa implementación desacoplada del núcleo financiero.
