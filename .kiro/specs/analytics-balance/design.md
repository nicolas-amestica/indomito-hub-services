# Diseño

## Decisiones y discrepancias
- ULID y JWT propio conforme a common-backend-rules/security-baseline; contracts-policy conserva referencias antiguas a UUID y auth por definir. No editar artefactos generados.
- program-form describe scaffolds vacíos y api-favorite: hoy api-program persiste favoritos y existen contratos/pagos. Reutilizar código vigente.
- `contract.payments.totalPassengers` son pagantes; schedule.totalPassengers del motor incluye liberados. Adaptación explícita.
- OpenAPI autoritativo existente: api-payment/openapi.fragment.json conforme a common-backend-rules; steering openapi-docs menciona pipeline Zod inexistente en package.json. Mantener fragmento y documentar esta decisión.

## Fronteras
El motor TypeScript existente (`baseAmount`, `calculateProgram`, rounding) calcula simulaciones; Go valida entradas, relaciones y operaciones, sin copiar fórmulas de cotización. Los totales derivados son estimaciones del cliente, nunca instrucciones de cobro. Finanzas reales se leen de collection, sin mutarlas.

## Persistencia y acceso
Tabla Programas existente para planificación; tabla Pagos exclusivamente como fuente de lectura. pk TRIP#<id>, sk ANALYTICS#CURRENT conserva estado y versión. Cada comando genera revisión inmutable ANALYTICS#REV#<version>, y recibo ANALYTICS#COMMAND#<ULID> para replay exacto. Transacción condicional sobre versión, sin Scan ni índices nuevos. Tamaño acotado por presupuesto; revisiones recuperables mediante Query. Categorías configurables por presupuesto. GET de contratos usa gsi-periodo-documental-index y procesa todas las páginas del alcance solicitado. Respuesta operativa agrupa cuentas del viaje en servidor, cuotas por vencimiento y eventos bancarios por período.

## Fórmulas y bases
- Costo = motor compartido sobre partidas incluidas, tasa histórica explícita, contingencia separada. Retención incluida en honorario bruto; no se agrega de nuevo.
- Venta contractual = condiciones vigentes del contrato/anexos cuando estén disponibles; venta simulada = pagantes × precio bruto × (1-descuento/100).
- Venta neta simulada = venta bruta/(1+IVA snapshot/100). Resultado operativo estimado = venta neta − costo en base declarada; margen = resultado/venta neta; recargo = resultado/costo. No utilidad neta.
- Pago de proveedor no incrementa compromiso. Pendiente = max(0, comprometido−pagado+recuperado). Recuperación esperada = acordada−recibida.
- Cobranza apilada excluyente: recibido aplicado, abono pendiente, cuotas no vencidas, cuotas vencidas. Cuotas separadas por antigüedad al día consultado.
- Caja = saldo de apertura respaldado por eventos + movimientos BANK del período. No equiparar net a resultado.
- Déficit actual = max(0, obligaciones pendientes proveedores + devoluciones pagables − caja al día consultado); es exposición inmediata si no hay vencimientos, no deuda vencida.
- Proyección: apertura verificable más flujos previstos fechados; máximo déficit = max(0,−mínimo saldo). Se exige declaración explícita de cobertura completa; de otro modo solo se muestra como proyección parcial.
- Equilibrio: enumeración discreta de pagantes, respetando liberados, capacidades y escalones. Sin solución se informa intervalo evaluado.

## Importación e integridad
Snapshot contractual con ID/versión/fecha. Importación reemplaza explícitamente un borrador, nunca añade duplicados ni modifica línea base. Si faltan tasas/impuestos históricos se exige completar con procedencia declarada; nunca se rellenan con configuración actual silenciosamente. Relaciones parciales a proveedores identificadas y validadas; una asignación no genera un costo nuevo.

## Interfaz
Cabecera compartida, PrimeNG + Tailwind, tokens corporativos. Ayudas junto a resultados: qué significa, fórmula y qué falta. Tabla de datos por gráfico y etiquetas de real/simulado. Carga diferida de Chart.js/Excel. Descartar respuestas obsoletas, mantener borrador tras fallo, confirmar cambios sin guardar y promoción/aprobación.

## Autorización existente
Sin cambios al authorizer: sus permisos por endpoint y método ya protegen /analitica mediante concesiones IAM. Las funciones privadas verifican UserIDFromContext, límites y reglas de operación; nunca aceptan identidad ni permisos del cuerpo.

Decisión acordada: servicio independiente `api-analytics`, mismo Gateway y authorizer sin modificaciones. Dominio de collection reutilizado como dependencia explícita de lectura, sin importar handlers de pagos ni ejecutar comandos financieros.

## Venta y agregación operativa verificada
La venta contractual operativa vigente se obtiene de cuentas actuales: cobros aplicados + deuda exigible − devoluciones por bajas aprobadas. Se distingue de la venta hipotética de escenarios y del ingreso contable. Si no existe cobranza, se usa el contrato aprobado; una consulta fallida no activa ese fallback. Los anexos de fechas se leen desde CONTRACT#id/TERMS#CURRENT sin modificar el documento original. La respuesta agrega vencimientos, cobros y movimientos por fecha/día; no descarga cuentas ni eventos individuales al navegador. El listado devuelve cinco viajes por página y omite contenido de escenarios alternativos.
