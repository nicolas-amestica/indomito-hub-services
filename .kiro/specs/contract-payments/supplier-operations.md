# Proveedores y servicios

Estado: CASH-02 terminado localmente. No desplegado.

## Datos conservados por proveedor

- Identificador interno, gira, nombre y servicio.
- Compromiso vigente y total efectivamente pagado.
- Devolución acumulada acordada y total efectivamente recuperado.
- Versión para control optimista; eventos y comandos inmutables de auditoría.

Una rebaja del servicio no inventa efectivo. `SUPPLIER_COMMITMENT_REVISED` puede reconocer una recuperación acordada, pero la caja cambia solamente con `SUPPLIER_REFUND_RECEIVED` respaldado por cartola.

## Operaciones

| Operación | Efecto de caja | Validación principal |
|---|---:|---|
| Crear compromiso | Ninguno | Proveedor, servicio y monto positivo |
| Revisar compromiso | Ninguno | Versión, respaldo/anexo y devolución acordada no decreciente ni superior a lo pagado |
| Pagar proveedor | Salida bancaria | No superar compromiso pendiente, fecha no futura y referencia única |
| Recibir devolución | Entrada bancaria | No superar devolución acordada pendiente, fecha no futura y referencia única |

## Acceso y costo

- Raíz: `PK=TRIP#<tripId>`, `SK=SUPPLIER#<supplierId>`.
- Eventos: `PK=TRIP#<tripId>`, `SK=SUPPLIER_EVENT#<supplierId>#<version>`.
- Listado paginado de 20 por la partición de gira; no usa Scan ni GSI.
- Los eventos bancarios escriben además las dos proyecciones de caja definidas en `treasury-operations.md`.

## Recuperación

La UI conserva el mismo ID de proveedor, `commandId` y cuerpo después de una respuesta incierta. El servidor deduplica el comando, protege la versión y reclama referencias bancarias globalmente. Cambiar la huella en un reintento se rechaza.
