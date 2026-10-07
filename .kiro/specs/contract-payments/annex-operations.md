# Anexos: preparación, previsualización y aplicación local

Estado al 2026-10-04: servicio interno implementado y probado con DynamoDB simulado. Incluye la aprobación técnica y aplicación recuperable, pero **todavía no tiene endpoints, formulario ni despliegue**. No usar las funciones internas como una aprobación contractual fuera del flujo administrativo que falta integrar.

## Garantías implementadas

- `PrepareAnnexDraft` recibe una operación administrativa identificada, gira, auditoría, bajas con versión esperada y altas con condiciones explícitas.
- IDs ULID canónicos; hasta 1000 propuestas por borrador. Se rechazan cuentas repetidas, altas con participante repetido dentro de la solicitud y mezcla de otra gira/anexo.
- La huella ordena altas y bajas por cuenta. Cambiar el orden no cambia la operación; cambiar importes, versiones, operador, fecha o motivo requiere otro ID.
- La baja propuesta conserva la cuenta, sus cobros y eventuales intentos abiertos como instantánea; no aprueba devoluciones. El alta propuesta no hereda dinero, descuentos ni intentos.
- Un reemplazo vincula bidireccionalmente una baja y un alta. Cada cuenta puede participar en una sola relación dentro del anexo; la relación no mueve abonos, cuotas pagadas, descuentos ni devoluciones.
- La preparación escribe **solo registros de borrador**. No modifica `ACCOUNT/META`, `TRIP/META`, contrato, RUT/código, recibos, referencias bancarias ni diario financiero.
- El plan continúa ACTIVE; los pagos legítimos pueden seguir llegando. Las propuestas pueden quedar obsoletas y la aprobación futura deberá revalidarlas bajo exclusión.
- La cabecera pasa de PREPARING_DRAFT a DRAFT únicamente cuando todas las propuestas fueron guardadas. DRAFT significa completo para revisar, **no aprobado ni aplicado**.

## Acceso y recuperación

| PK | SK | Contenido |
|---|---|---|
| `TRIP#<tripId>` | `ANNEX#<annexId>` | Huella, estado, versión, contador y auditoría original. |
| `TRIP#<tripId>` | `ANNEX#<annexId>#PROPOSAL#<accountId>` | Propuesta inmutable de cuenta/evento y huella; no es un evento financiero confirmado. |

Creación de cabecera: una escritura condicional y comprobación transaccional de plan ACTIVE. Cada propuesta: una escritura única, avance de cabecera con CAS y comprobación ACTIVE (tres acciones). Finalización: cambio de estado con CAS y comprobación ACTIVE.

Reenviar la **misma solicitud completa y auditoría original** reanuda los pasos faltantes. No regenerar `RecordedAt` para un reintento. Respuestas perdidas se recuperan leyendo la cabecera/propuesta; los contadores no se incrementan dos veces.

Si una cuenta cambia antes de preparar su propuesta, se rechaza la versión esperada y el borrador puede quedar incompleto, sin afectar dinero. La futura UI debe permitir abandonar ese borrador y crear otro con revisión actualizada. No hay eliminación automática ni TTL de esta auditoría.

`GetAnnexDraft`: un GetItem consistente. `PreviewAnnexDraft`: un GetItem de cabecera, una Query consistente de hasta 20 propuestas y hasta 20 GetItem de cuentas actuales. El cursor contiene solo un ULID y el servidor reconstruye partición/prefijo. No hay Scan, GSI ni lectura global.

La previsualización muestra deuda propuesta agregada/cancelada, reemplazo y versiones esperada/actual. `stale=true` advierte cambios posteriores. El resumen recorre todas las páginas y entrega altas, bajas, liberados, reemplazos y delta neto de cobranza; si falta o se corrompe una propuesta, no entrega un total parcial. Los importes pertenecen al borrador: no se recalculan silenciosamente con un pago nuevo. No son caja ni totales consolidados del grupo. `stale=false` tampoco garantiza una aprobación posterior: existe concurrencia después de leer.

## Aprobación y aplicación

`ApplyAnnex` bloquea primero el plan como `UPDATING_ROSTER`, enlaza `pendingAnnexId` y cambia la cabecera desde `DRAFT` a `VALIDATING`. Antes del primer efecto vuelve a leer **todas** las propuestas y cuentas con consistencia fuerte. Una versión obsoleta, cuenta faltante, identidad ocupada o relación inválida marca el anexo `REJECTED` y devuelve el plan a `ACTIVE` sin aplicar altas ni bajas.

Superada la prevalidación, la cabecera pasa a `APPLYING`. Cada propuesta se confirma en una transacción independiente que exige que el mismo anexo mantenga el bloqueo. Esa transacción contiene:

- escritura condicional de la nueva versión de la cuenta;
- evento inmutable de alta o baja, sin mover pagos, descuentos, fondos en revisión ni devoluciones entre personas;
- marca `ANNEX#<annexId>#EFFECT#<accountId>` para impedir aplicar dos veces;
- incremento CAS del contador `applied` de la cabecera;
- para un alta con RUT válido, relación `RUT#<HMAC>` creada condicionalmente en la misma transacción.

Una baja deja la cuenta `INACTIVE` pero conserva su lookup, historial y obligaciones/devoluciones, para que el pasajero pueda consultar comprobantes y resultados posteriores. Un alta parte con su propia cuenta e importes aprobados; un reemplazo es solamente trazabilidad bidireccional.

Si el RUT del alta ya apunta a una participación de la misma gira, el anexo solo se aprueba cuando esa cuenta es exactamente la baja vinculada por un reemplazo. Se aplican primero las bajas y después las altas; entonces el lookup cambia mediante `attribute_not_exists(pk) OR accountId = <cuenta anterior>`. Nunca puede desplazar el acceso de otra persona ni reutilizar su cuenta. La participación anterior conserva todos sus importes y queda enlazada con `nextParticipation`; la nueva queda enlazada con `previousParticipation` y empieza financieramente en cero salvo las condiciones nuevas pactadas. La nómina administrativa lista ambas participaciones sin GSI. Una sesión breve emitida antes del reingreso continúa limitada a la cuenta histórica; una consulta nueva RUT+código resuelve la participación activa nueva.

Cuando `applied == expected`, una última transacción cambia la cabecera a `APPLIED` y libera el plan como `ACTIVE`, eliminando `pendingAnnexId`. No existe publicación parcial: mientras falte un efecto, las demás mutaciones financieras observan `UPDATING_ROSTER` y deben reintentarse. Las notificaciones Khipu ya recibidas permanecen durables y su trabajador continúa después de la liberación.

Un corte antes o después de cualquiera de esas transacciones se recupera repitiendo la misma operación. Llamadas simultáneas pueden recibir conflicto mientras otra avanza; consultar/reintentar devuelve finalmente el estado persistido sin duplicar efectos. La futura API debe representar ese estado, no interpretar un conflicto transitorio como rechazo del anexo.

## API administrativa local

Las rutas requieren `paymentAccess=admin`; no comparten la sesión del portal público:

| Método y ruta | Uso |
|---|---|
| `POST /pagos/giras/{tripId}/anexos` | Preparar/reanudar borrador con una clave ULID. |
| `GET /pagos/giras/{tripId}/anexos/{annexId}` | Recuperar estado y avance durable. |
| `GET .../{annexId}/propuestas?cursor=` | Revisar hasta 20 efectos por página. |
| `GET .../{annexId}/impacto` | Recalcular el resumen completo antes de aprobar. |
| `POST .../{annexId}/aplicacion` | Prevalidar, aplicar y publicar o reanudar una aplicación interrumpida. |
| `GET /pagos/giras/{tripId}/pasajeros?cursor=` | Listar 50 integrantes de la nómina vigente para administración. |
| `POST /pagos/giras/{tripId}/pasajeros/migracion` | Preparar una nómina histórica desde contrato+SETUP, sin aceptar integrantes desde el navegador. |
| `POST /pagos/giras/{tripId}/pasajeros/cierre` | Cerrar definitivamente altas/bajas/reemplazos, sin cerrar operaciones financieras. |

El navegador no puede enviar actor, fecha de auditoría, `tripId` interno de las altas, `annexId` interno ni claves HMAC. El handler toma el actor del authorizer, fija el tiempo del servidor y deriva el HMAC desde el RUT. El RUT crudo sirve solamente durante esa conversión y no se incluye en el registro del borrador. La auditoría de preparación y la de aprobación se guardan separadas. Ante una respuesta perdida, la misma clave reutiliza la fecha ya persistida; cambiar actor, motivo o contenido produce conflicto de replay.

La nómina administrativa vive en la misma partición de la gira como `MEMBER#<accountId>`. Contiene identidad visible, estado activo/liberado y versión de cuenta; se crea o actualiza dentro de la misma transacción que la cuenta, por lo que el formulario no necesita reconstruir integrantes desde el contrato ni consultar todas las cuentas. Es información privada y no forma parte de las rutas del portal. Los planes creados antes de esta proyección requieren un backfill idempotente y auditado; no se inventará la proyección mediante un replay que cambie su huella histórica.

El formulario administrativo está disponible en `/cobranza/giras/{tripId}/anexos/nuevo`. Primero congela un borrador y presenta altas, bajas, reemplazos, deuda agregada/cancelada y versiones obsoletas. La aplicación exige una segunda confirmación. El ID queda en `?anexo=` para recuperar la revisión; la aprobación usa una clave derivada del propio anexo y el backend devuelve el motivo ya persistido, de modo que una recarga durante `VALIDATING` o `APPLYING` puede reanudar exactamente la misma operación.

### Backfill de planes anteriores

Los planes nuevos guardan `rosterSchemaVersion=1`. Una consulta de nómina rechaza planes sin esa versión para no presentar una lista vacía o parcial como si fuera válida. La pantalla ofrece entonces una migración explícita. El servidor vuelve a leer el contrato aprobado y la instantánea `SETUP`, exige la misma versión y los mismos IDs, y construye internamente los perfiles.

La migración usa `TRIP#<tripId> / ROSTER_MIGRATION#0001` como control reanudable. Cada integrante se escribe junto con un control de versión de su cuenta y el avance CAS; pagos concurrentes pueden obligar a reintentar, pero no se pierden ni se sobrescriben. Solamente al completar todas las filas se publica `rosterSchemaVersion=1`. La huella histórica del plan, las cuentas, eventos, saldos, comprobantes y lookup permanecen intactos. Para N pasajeros usa una transacción inicial, N transacciones de hasta cuatro acciones y una publicación final; no agrega GSI ni Scan.

### Cierre formal de nómina

El cierre exige nómina proyectada completa, plan ACTIVE y ausencia de un anexo pendiente. Una única transacción CAS marca `rosterClosed=true`, conserva la auditoría de servidor y escribe `ROSTER_CLOSURE#<commandId>` como evento inmutable. Repetir exactamente el comando no vuelve a escribir; cambiar operador o motivo con la misma identidad se rechaza.

Preparar un borrador y comenzar su aprobación verifican transaccionalmente que la nómina permanezca abierta. Un borrador preparado antes del cierre tampoco puede aplicarse después. El cierre no cambia el estado ACTIVE del plan: depósitos manuales, pagos tardíos, comprobantes, fondos en revisión y devoluciones siguen funcionando. La pantalla administrativa exige motivo y una confirmación separada, y después muestra fecha/motivo persistidos en vez de los controles de anexo.

## Costo y límites

- Para N propuestas nuevas: N+2 transacciones; 2N+2 escrituras de items y N+2 comprobaciones transaccionales, además de lecturas de preparación/verificación. Las operaciones transaccionales consumen más capacidad que escrituras simples; esto no es una estimación en dinero ni una promesa de Free Tier.
- Reintentos completos ya guardados no escriben nuevamente, pero sí leen. La UI futura debe usar consulta de estado para seguimiento, no reenviar continuamente el lote.
- Prueba local con 150 propuestas: 152 commits, máximo tres acciones por transacción; replay con orden invertido sin escrituras nuevas.
- El límite de 1000 es del servicio interno, no un límite de payload HTTP aprobado. Al integrar API se debe definir carga por páginas/tamaño y tiempos de Lambda; no aumentar indiscriminadamente el límite HTTP existente.
- Aplicar N propuestas confirmadas agrega hasta N transacciones de efectos y una transacción de publicación. Cada efecto usa cinco acciones, o seis si crea el lookup HMAC de una nueva identidad; permanece bajo el máximo de 100 acciones de DynamoDB.
- Sin recursos AWS nuevos ni trabajadores periódicos en esta etapa.

## Verificación local

- Preservación exacta de la cuenta activa; alta no publicada; replay sin modificaciones.
- Interrupción antes de cada commit, respuesta perdida después de commit y recuperación.
- Ocho invocaciones concurrentes de la misma solicitud sin duplicar propuestas/contadores.
- Identidades duplicadas dentro del lote, alta con cuenta ya existente, versión obsoleta, otra gira e ID/cursor inválido.
- Contexto cancelado y plan no activo no crean borrador.
- Paginación 20/20/1 y rechazo de previsualización parcial.
- Pago recibido después del borrador: continúa registrado, propuesta original intacta y previsualización marcada obsoleta.
- Rechazo integral antes de efectos cuando una versión o identidad quedó obsoleta.
- Interrupción en cada frontera transaccional de aplicación y posterior recuperación hasta `APPLIED`.
- Reintentos simultáneos sin duplicar cuentas, eventos, efectos ni lookup; una repetición final obtiene el resultado durable.
- `go test -race ./...`, `go vet ./...`, lint; suite de anexos repetida 20 veces. No reemplaza pruebas contra DynamoDB real.

## Pendientes antes de uso administrativo

1. Límites de liberados, anexos de servicios y corte de fechas. La validación interna de IDs no sustituye verificar documentos de identidad.
2. Prueba AWS de autorización/IAM, DynamoDB real y recuperación ante cortes.
3. Revisión visual autenticada; un hash HMAC no reemplaza la comprobación humana de la nómina.
4. Revisión de costos/carga; aún no habilitar cobros públicos.
