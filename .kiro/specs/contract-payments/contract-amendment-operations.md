# Anexos contractuales de fechas y servicios

Estado local al 2026-10-05. No desplegado.

## Frontera funcional

Este anexo modifica exclusivamente fechas, duración y lista completa de servicios. No contiene ni acepta precio, abono, cuotas, pagos, descuentos o devoluciones. El contrato y su PDF aprobado permanecen inmutables; cada anexo conserva instantáneas `before` y `after` y genera su propio PDF definitivo.

Un anexo parte de las condiciones efectivamente vigentes, no siempre del contrato original. `CONTRACT#<id> / TERMS#CURRENT` guarda esa proyección con una revisión creciente. La creación comprueba esa revisión y reserva `AMENDMENT_PENDING`, por lo que solo puede existir un borrador pendiente por contrato. La aprobación vuelve a comprobar contrato, borrador, revisión vigente y reserva dentro de una sola transacción.

## Patrones de acceso

| PK | SK | Uso |
|---|---|---|
| `CONTRACT#<id>` | `AMENDMENT#<id>` | Documento histórico completo, estado y auditoría. |
| `CONTRACT#<id>` | `AMENDMENT_PENDING` | Exclusión de un único borrador pendiente. |
| `CONTRACT#<id>` | `TERMS#CURRENT` | Condiciones vigentes y revisión para el siguiente anexo. |
| `TRIP#<id>` | `TERMS#CURRENT` | Proyección mínima en cobranza para futuras alertas y corte operativo. |

No se usa `Scan` ni GSI. El listado hace `Query` consistente por una sola partición, páginas de 50 y cursor ULID. Crear usa dos lecturas por clave y una transacción de cuatro acciones. Aprobar almacena primero un PDF de contenido direccionado y luego ejecuta una transacción de cinco acciones, o seis cuando la proyección de cobranza está habilitada. Un fallo competitivo puede dejar un objeto S3 huérfano sin publicar; no puede aprobar parcialmente DynamoDB ni reemplazar un PDF ya referenciado.

## API administrativa

| Método y ruta | Resultado |
|---|---|
| `POST /contratos/{id}/anexos` | Crea/reintenta el único borrador pendiente con ID aportado por el cliente. |
| `GET /contratos/{id}/anexos?cursor=` | Lista el historial de esa partición. |
| `POST /contratos/{id}/anexos/{anexo}/aprobacion` | Aprueba, publica condiciones vigentes y registra el PDF. |
| `GET /contratos/{id}/anexos/{anexo}/pdf` | Verifica SHA/tamaño y entrega URL temporal de 15 minutos. |

El servidor obtiene operador y horas de auditoría desde el contexto. El formulario Angular reutiliza la última instantánea aprobada, recupera un borrador pendiente después de recargar y exige confirmación explícita antes de aprobar.

## Límites pendientes de AWS

- Validar transacciones entre tabla de contratos y tabla de pagos con IAM real.
- Verificar S3, integridad, URLs firmadas y PDF representativo renderizado.
- Definir limpieza operativa de objetos S3 huérfanos que nunca quedaron referenciados.
- La proyección deja disponible la fecha efectiva; el panel de alertas se implementa en `CASH-05`.
