# Analítica → Detalle balance

## Repos involucrados
| Repositorio | Responsabilidad |
|---|---|
| API / api-analytics | Persistencia de presupuestos y escenarios; lectura consolidada contractual y operativa |
| Hub | Feature analytics, reutilización del motor, edición, gráficos y Excel |
| Orquestador | Contrato y decisiones compartidas |

## Requisitos y aceptación
- Ruta /analitica/detalle-balance; LV1 Analítica / LV2 Detalle balance. Bootstrap idempotente para perfil exacto ADM, sin alterar otros permisos. Preparación local; no ejecutar bootstrap ni despliegues.
- Catálogo completo de contratos aprobados del período documental, incluidos viajes sin deuda. Fechas de salida independientes.
- Presupuesto manual o importado del snapshot contractual, sin consultar tasas actuales para reconstruir históricos.
- Guardar produce borrador/estimación, aprobar es explícito y conserva línea base y revisiones previas. Actor/fecha/motivo y versión obligatorios.
- Escenarios independientes, precio fijo o precio objetivo, promoción explícita. Sin efectos sobre contratos/cobros/proveedores.
- Servicios con bases y días/noches propios, liberados, costos por escalones y límites de capacidad. Cero pagantes inválido.
- Mostrar déficit actual y máximo déficit previsto; fechas/saldos ausentes hacen la proyección incompleta.
- Cinco secciones: Resumen, Presupuesto, Simulación, Cobranza y Caja/proveedores. Explicaciones en lenguaje cotidiano, severidades con texto, temas claro/oscuro y 320 px.
- Dieciocho visualizaciones definidas en la solicitud original, con alternativa tabular, fuente, unidad, período y estados incompletos.
- Tabla consolidada, filtros y Excel coherentes. Datos ausentes nunca se convierten en cero.
- Validar build, check:buttons, pruebas de fórmulas, autorización y concurrencia. No afirmar aceptación en AWS.

Decisión acordada: servicio independiente `api-analytics`, mismo Gateway y authorizer sin modificaciones. Dominio de collection reutilizado como dependencia explícita de lectura, sin importar handlers de pagos ni ejecutar comandos financieros.
