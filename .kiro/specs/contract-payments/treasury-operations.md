# Tesorería y conciliación bancaria

Estado: desarrollo local terminado para CASH-01. No desplegado ni validado contra DynamoDB/Khipu reales.

## Límites contables

- Una cuota confirmada por Khipu acredita fondos del cliente contra `PROVIDER_RECEIVABLE`; todavía no incrementa `BANK`.
- La caja cambia únicamente con eventos equilibrados que contienen una partida `BANK` y fecha efectiva completa.
- Una liquidación observada en cartola mueve el bruto desde `PROVIDER_RECEIVABLE`; separa la comisión real en `PAYMENT_FEES` y el neto en `BANK`.
- El abono grupal, la cuota manual y las devoluciones confirmadas ya son movimientos bancarios. No crean una liquidación Khipu adicional.
- Una reversa Khipu anterior a la liquidación puede revertir la cuenta por cobrar al proveedor. Si algún bruto ya fue liquidado, queda en revisión: se requiere registrar la salida bancaria real para no falsear caja.
- Esta proyección operativa no reemplaza contabilidad, DTE, libro mayor ni conciliación tributaria.

## Patrones de acceso

| Consulta | Clave DynamoDB | GSI |
|---|---|---|
| Caja histórica de una gira | `PK=TRIP#<tripId>`, `SK=CASH#<fecha>#<commandId>` | No |
| Movimientos de todas las giras por mes | `PK=CASH#AAAA-MM`, `SK=TRIP#<tripId>#<fecha>#<commandId>` | No |
| Liquidaciones Khipu de una gira | `PK=TRIP#<tripId>`, `SK=SETTLEMENT#khipu:<paymentId>` | No |
| Referencia bancaria global | `PK=REFERENCE#bank:<referencia>`, `SK=META` | No |

Las dos proyecciones de caja se escriben en la misma transacción que el evento financiero. No se usa `Scan`. La consulta por gira pagina movimientos en bloques de 50 y liquidaciones en bloques de 20.

## API administrativa

- `GET /pagos/tesoreria/giras/{tripId}/liquidaciones?cursor=...`
- `POST /pagos/tesoreria/giras/{tripId}/liquidaciones/{paymentId}`
- `GET /pagos/tesoreria/giras/{tripId}/caja?from=AAAA-MM-DD&to=AAAA-MM-DD`

El POST exige versión de liquidación, monto bruto, comisión real, referencia bancaria, fecha efectiva, motivo e identificador de comando. El servidor fija actor y fecha de registro, y deduplica tanto el comando como la referencia bancaria.

## Recuperación segura

1. Ante timeout, reenviar exactamente el mismo `commandId` y cuerpo.
2. No cambiar monto, comisión, fecha ni referencia durante el reintento.
3. Un mismo comando con otra huella se rechaza.
4. Una referencia bancaria reclamada por otro movimiento se rechaza.
5. La pantalla congela el formulario después del primer envío incierto y conserva la solicitud en memoria para reintentar.

## Evidencia local

- Dominio, persistencia y handlers de `api-payment` probados con detector de carreras.
- Idempotencia, referencia duplicada, paginación 20+1, proyecciones de abono grupal y liquidación probadas.
- Una confirmación Khipu crea una liquidación pendiente; un ingreso bancario directo no la crea.
- Angular compila con AOT y las pruebas de servicio/pantalla de tesorería pasan.
- Declaraciones IAM ampliadas a las claves `TRIP#*` y `CASH#*` estrictamente necesarias.

## Pendiente fuera de CASH-01

- Validación real de DynamoDB/IAM y cartola Khipu en DEV.
- Egresos y recuperaciones de proveedores (CASH-02).
- Consolidado mensual entre giras, proyección versus efectivo y paneles de devoluciones/alertas.
- Flujo bancario explícito para reversas ocurridas después de una liquidación.

## Extensión CASH-03

La vista por gira agrega, sin sumarlos a caja: deuda por cobrar, fondos sin aplicar, devoluciones pendientes, compromisos pendientes con proveedores y recuperaciones esperadas. Las posiciones de pasajeros se leen por la nómina de la gira con hasta diez lecturas directas concurrentes; no se crea un índice global.

El consolidado consulta `PK=CASH#AAAA-MM` para cada mes de un rango máximo de doce meses y agrupa por gira. Reporta entradas, salidas, neto y comisiones reales del período. Solo incluye giras con movimientos dentro del rango y declara expresamente que el neto no es el saldo de la cuenta bancaria.
