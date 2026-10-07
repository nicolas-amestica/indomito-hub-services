# Auditoría de costo y patrones de acceso

## Resultado

El diseño evita `Scan` y no agrega GSI a la tabla de pagos. DynamoDB usa `PAY_PER_REQUEST`; DEV no tiene PITR y producción sí. Las funciones Go usan ARM64, 128 MB por defecto, sin concurrencia provisionada ni versiones publicadas, con logs por 14 días. El trabajador de comprobantes usa ARM64/256 MB, concurrencia reservada 2 y lote 1 para limitar duplicados y ráfagas SMTP.

## Costo relativo por flujo

| Flujo | Lecturas | Escrituras dominantes | Observación |
|---|---:|---:|---|
| Consulta pública | 4 claves directas | límite de intentos | Sin GSI; sesión evita repetir identificación en cada pantalla. |
| Abrir checkout | cuenta/plan/intento | cuenta, intento y comando | La consulta a Khipu domina el costo externo. |
| Confirmar pago | claves directas | transacción de cuenta, evento, recibo, job, caja y deduplicación | Más escrituras, necesarias para exactitud e idempotencia. |
| Listados por cuenta/gira | 1 `Query`, páginas 20/50 | ninguna | Particiones acotadas; el cliente no hace fan-out. |
| Puesta en marcha/anexos/grupos | páginas/lotes | transacciones pequeñas por pasajero | Costo lineal y reanudable; evita transacciones gigantes. |
| Comprobante | 1–2 lecturas/actualizaciones | un PDF S3 y correo | Backfill usa páginas 10 y no envía correo. |
| Caja consolidada | una partición por mes, máximo 12 | ninguna | No es saldo bancario; rango obligatorio. |

## Retención

La tabla usa TTL únicamente para datos efímeros que tengan `expiresAt`; eventos financieros, comandos, caja y comprobantes no deben expirar automáticamente. El bucket privado de comprobantes tiene versionado, `Retain` y sin expiración: es una decisión de trazabilidad que aumenta almacenamiento lentamente. No aplicar lifecycle destructivo hasta acordar retención legal y respaldo.

## Riesgos y límites

- Free Tier no equivale a costo cero ni debe asumirse permanente.
- DynamoDB Streams invoca dos consumidores filtrados; los filtros evitan procesamiento de filas ajenas, pero el stream conserva el costo propio del servicio.
- X-Ray está activo globalmente y puede generar costo de trazas; revisar muestreo antes de producción.
- SMTP, Khipu, DNS/CDN y transferencia S3 tienen precios externos que deben cotizarse con tráfico real.
- La cantidad de escrituras por pago es deliberadamente mayor que un CRUD simple: eliminar deduplicación, diario, caja u outbox reduciría costo a cambio de perder exactitud.

## Alarmas mínimas antes de habilitar

Configurar AWS Budget mensual y alarmas de Lambda Errors/Throttles, DynamoDB throttling/system errors, edad/fallos de Streams, mensajes visibles y edad de SQS, errores API 5xx, almacenamiento S3 y fallos de conciliación. Los umbrales monetarios requieren el presupuesto del usuario; no se inventan en infraestructura.

La medición final debe ejecutar escenarios de 30, 100 y 300 pasajeros y registrar RCUs/WCUs, duración Lambda, bytes de logs/PDF y llamadas Khipu por operación.
