# Diseño y operación

## Arquitectura

Webhook firmado → S3 privado (original y reporte financiero normalizado) → transacción inbox + índice mensual + trabajo → Lambda programada → resultados por pago y asientos idempotentes → vista administrativa.

Se reutilizan la tabla de pagos y el bucket privado de comprobantes, con permisos limitados al prefijo `renditions/{stage}/`. No se crean tablas, índices, colas ni consumidores adicionales de Streams. El trabajador invoca cada minuto, consulta hasta 10 trabajos vencidos y procesa hasta 50 pagos por lote/invocación. Una configuración ausente deshabilita el trabajador y el receptor responde 503. No se reconoce evidencia que no pudo persistirse.

El consumo de base es aproximadamente 43.200 invocaciones y lecturas de configuración al mes, más S3/DynamoDB por reporte y pago. Revisar costo y métricas reales del ambiente; no se promete costo cero. Una sola concurrencia reservada limita el trabajo programado. No se usa Scan.

## Identidad y recuperación

- ID interno determinista sobre ambiente, destinatario, banco, cuenta y drnId.
- Hash del reporte normalizado (orden de pagos y montos canónicos); no depende de iat ni de la firma del reenvío.
- Cada asiento usa un comando determinista rendición/pago y una asignación `rendition:{id}:{paymentId}`; no reutiliza ni inventa una referencia bancaria.
- El checkpoint y resultado se actualizan juntos. Si el asiento quedó confirmado antes de perder la respuesta, la siguiente ejecución recupera el comando existente.
- Los trabajos vencidos rotan a una nueva fecha para evitar bloquear otros lotes. Reintentos de error cada cinco minutos; después de 12 fallos, revisión administrativa. Un pago todavía no confirmado se espera; tras agotar la espera pasa a revisión y se continúa el resto del lote.
- Reprocesar archiva el estado anterior en `RUN#version` y conserva evidencia y asientos anteriores. La configuración vigente puede detener APPLY cambiando modo o revisión.
- Una reversa verifica transaccionalmente que el pago todavía no fue liquidado; una rendición verifica que no exista resolución del intento. Se evita la carrera entre ambos procesos.

## Caja y comisiones

OBSERVE nunca escribe caja. APPLY exige revision, responsable, evidencia y selección explícita `REPORT_END` después de verificar con Khipu qué representa esa fecha en la cuenta real. No se toma paymentDate como fecha bancaria. La UI distingue evidencia de proveedor de cartola.

La lista `deductedFeeSchemes` permite únicamente esquemas cuyo importe khipuFee se comprobó como descuento efectivo del abono. `deferredFeeSchemes` admite esquemas cobrados por separado únicamente si khipuFee es cero: se ingresa el bruto, sin inventar comisión. La factura/cobro posterior de la comisión queda fuera de este proceso. Los nombres se comparan exactamente, no se interpretan tarifas UF ni textos para estimar cobros.

Comisiones de integrador no nulas, pagos ya conciliados, importes distintos, reversas y esquemas desconocidos requieren revisión. No se ajusta automáticamente un pago conciliado manualmente, ni se presupone que una coincidencia de monto identifica al pasajero.

## Límites externos

La documentación pública revisada no garantiza una API de recuperación histórica de rendiciones ni la generación de rendiciones en DemoBank. Hay que confirmar estas capacidades con la cuenta developer/Khipu. El receptor permite reenviar el sobre firmado original válido; la API privada permite reprocesar evidencia ya recibida. No admite un JSON arbitrario para fabricar ingresos.

La detección de notificaciones nunca recibidas requiere contrastar los pagos en tránsito con reportes Khipu. El receptor no puede detectar por sí solo un reporte que no llegó; no se ha implementado una consulta periódica al proveedor sin una API oficial confirmada. Antes de activar APPLY, definir procedimiento de control de pendientes y recuperación por lote. Una conciliación independiente de cartola requiere otra integración bancaria.
