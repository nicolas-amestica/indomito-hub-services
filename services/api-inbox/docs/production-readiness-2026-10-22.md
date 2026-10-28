# Puesta en producción de API Inbox y WhatsApp Cloud API

Fecha de auditoría: 2026-10-22
Última actualización: 2026-10-23

Este reporte compara `api-inbox` con `portal-admin-sls-dev-pri-usw2/services/api-whatsapp-agent`. No autoriza despliegues. El cambio del callback de Meta está siendo realizado directamente por el responsable de la cuenta.

## Veredicto

`api-inbox-prd` está desplegado y su infraestructura base está operativa. El código local ya cierra la ruta principal de estados de entrega y derivación humana, pero esos cambios aún no están publicados. La migración de conversaciones legacy queda explícitamente fuera de alcance para esta puesta en marcha.

Callback objetivo estable:

```text
https://api.girasindomito.cl/webhooks/whatsapp
```

La URL default de API Gateway queda solo como respaldo técnico; Meta debe usar el dominio productivo estable.

## Evidencia verificada

| Área | Estado observado |
| --- | --- |
| Stack | `api-inbox-prd` en `CREATE_COMPLETE` desde 2026-10-17 |
| Lambdas | 25 funciones `api-inbox-prd-*`, todas `Active/Successful` |
| Webhook | GET y POST públicos registrados en el API Gateway compartido |
| Handshake | GET con el verify token productivo retorna HTTP 200 y replica `hub.challenge` |
| Dominio | DNS, TLS y ruta de `api.girasindomito.cl` operativos |
| SSM | Siete parámetros Inbox presentes como `SecureString`; WhatsApp tiene los cuatro campos obligatorios |
| Meta | Phone Number ID válido, plataforma `CLOUD_API`, calidad `GREEN`; lectura comprobada con Graph v21 y v26 |
| Agente | `enabled=true`; respuesta, cotizaciones y estados `read` confirmados en Go |
| Gemini | El modelo configurado responde mediante Interactions API; `generateContent` retorna HTTP 404 para esta credencial |
| Calendar | Refresh OAuth correcto, calendario legible y consulta del 2026-10-23 exitosa |
| Assets | Bucket accesible; certificado Sernatur y presupuestos presentes |
| Persistencia | Tabla destino activa y Stream habilitado; todavía no contiene la carga legacy |
| Colas | Inbound, agent, workflow y realtime vacías; event source mappings habilitados |
| IAM funcional | Módulos `COM`/`INBOX` y permisos para `ADM` existentes |
| Tráfico 24 h | Legacy POST: 158 invocaciones; target POST: solo la invocación de auditoría |
| Migración dry-run | 390 conversaciones, 3.014 mensajes y 4.186 ítems detectados; cero escrituras |
| Calidad local | `golangci-lint`, `go test -race`, `go vet`, contratos y build ARM64 exitosos; el siguiente cambio incorpora una función HTTP número 26 |
| Preflight AWS | SSO `pa-prd` renovado; cuenta `576394965493` y configuración PRD resuelta correctamente con Serverless 4.6.2 |

## Lista P0: puesta en marcha del webhook

### 1. Procesar estados de entrega de WhatsApp

El legacy extrae `statuses` y actualiza de forma monótona `sent -> delivered -> read`, conserva `failed`, correlaciona por WAMID y publica el cambio por WebSocket. Al iniciar esta auditoría, el target declaraba esos estados en el modelo pero ignoraba los eventos `statuses`; esa brecha quedó corregida y desplegada.

Lista:

- [x] Decodificar `statuses` en el mismo cuerpo autenticado por `X-Hub-Signature-256`.
- [x] Crear el índice idempotente `WAMID#<id>` atómicamente al confirmar cada envío; ya existía en `DynamoSendRepository.Commit`.
- [x] Aplicar progresión monótona `sent -> delivered -> read`, mantener `failed` terminal y limitar el motivo de falla a 500 caracteres.
- [x] Publicar `message.delivery.updated` por realtime respetando la secuencia de detalle.
- [x] Ignorar de forma segura WAMID desconocidos, incluidos recibos de mensajes legacy no migrados.
- [x] Reintentar una vez, con lectura consistente y espera acotada de 400 ms, si el callback llega antes de que `Commit` cree el índice WAMID; mantiene paridad con la protección del legacy.
- [x] Cubrir decodificación, `failed`, duplicados, WAMID desconocido, relectura acotada, progresión y orden invertido.
- [ ] Para tolerancia superior a legacy, persistir temporalmente recibos que sigan sin índice después de la relectura y reconciliarlos tras el commit.
- [ ] Completar prueba explícita de transacción cancelada/reintento de secuencia realtime.

### 2. Persistir la derivación al equipo humano

Al iniciar esta auditoría, `sin_respuesta` y `derivar_equipo` solo detenían la respuesta del turno. La persistencia de estado, motivo y realtime quedó corregida y desplegada. La reactivación explícita y los datos recopilados entre turnos ya están implementados y probados localmente; falta desplegar esta revisión.

Lista:

- [x] Mutar atómicamente conversación, proyecciones de lista/búsqueda y realtime a estado `derived`.
- [x] Guardar motivo normalizado y etiqueta de derivación con límites explícitos.
- [x] Impedir respuestas automáticas mientras la conversación siga derivada.
- [x] Derivar también ante error irrecuperable de Gemini sin perder el mensaje entrante.
- [x] Agregar prueba de la mutación transaccional y del evento realtime.
- [x] Persistir por 30 días los datos estructurados de reunión/cotización en `CONV#<telefono>/AGENT_STATE`, cargarlos en el turno siguiente y limpiar los datos de reunión después de crearla; pendiente smoke tras despliegue.
- [x] Implementar y probar una reactivación explícita `derived -> bot` autorizada para el equipo mediante `PATCH /conversaciones/{telefono}/estado-agente`, con acción de confirmación en la UI; pendiente despliegue.

### 3. Migración de conversaciones legacy: fuera de alcance

- [x] Excluir la migración histórica de esta puesta en marcha por decisión del responsable.
- [x] No ejecutar `apply`, delta ni rollback del migrador.
- [x] Aceptar que la bandeja nueva comienza sin el historial legacy y que los estados de WAMID antiguos se ignoran.
- [ ] Retomar el endurecimiento del migrador en una iniciativa posterior, antes de cualquier ejecución real.

### 4. Validar el callback real y la suscripción WABA en Meta

Al momento de la auditoría, las métricas mostraban tráfico real en legacy y solo una invocación de prueba en el target. El responsable está cambiando el callback en Meta; el estado posterior debe confirmarse antes del smoke.

Lista:

- [x] Callback cambiado por el responsable a `https://api.girasindomito.cl/webhooks/whatsapp`; las invocaciones productivas ya llegan al target.
- [x] Diagnosticar el primer envío real: el agente genera la respuesta, pero Meta devuelve `131047: Re-engagement message`.
- [x] Reemplazar en `/indomito/prd/inbox/whatsapp-config` el Phone Number ID de prueba por el productivo.
- [x] Confirmar operativamente el access token y número productivos: el agente Go respondió y Meta informó estado final `read`.
- [ ] Confirmar en Meta Developers la app productiva, WABA, número y suscripción al campo `messages`.
- [x] Ejecutar un evento real firmado y comprobar webhook, reserva, Stream, SQS, materialización, agente y callbacks de entrega.
- [x] Enviar un mensaje real controlado y comprobar respuesta del agente Go con estado final `read`.
- [x] Confirmar que Meta apunta al target: hubo webhook, inbound y agente en `api-inbox`; legacy no registró invocaciones durante la prueba.

### 5. Desplegar y verificar el hardening local

Lista:

- [x] Actualizar Graph API de v21 a v26 para envíos, uploads y descargas.
- [x] Responder HTTP 401 ante firma inválida y HTTP 200 ante un evento firmado pero irrelevante.
- [x] Agregar pruebas unitarias para firma, eventos irrelevantes, estados de entrega y derivación.
- [x] Ejecutar nuevamente `golangci-lint`, `go test -race`, `go vet`, contratos y build ARM64 de las 26 funciones con el diff actual.
- [x] Resolver y revisar `serverless print` PRD con la release 4.6.2 declarada por el servicio y credenciales `pa-prd` vigentes.
- [x] Fijar Serverless en `4.45.0` como dependencia exacta; `npx serverless --version`
  resuelve esa versión sin depender de una release global.
- [x] Stack publicado manualmente en PRD por el responsable.
- [x] Lambdas principales del flujo verificadas `Active/Successful`, con actualización productiva del 2026-10-23.

### 6. Cerrar el flujo de reuniones del agente IA

El mensaje real `Puedes agendar un reunión para hoy?` fue persistido, pero el worker terminó en ~340 ms sin responder. La conversación quedó `derived` con motivo `agent_error`; no hubo mensajes en las DLQ. La causa concreta es que el cliente usaba el endpoint legacy `generateContent`, que devuelve HTTP 404 para la credencial productiva, mientras el mismo modelo funciona correctamente mediante Gemini Interactions API. Calendar no fue la causa: OAuth y lectura del calendario respondieron correctamente.

En el primer smoke posterior al despliegue de Interactions, Gemini devolvió HTTP 400 antes de ejecutar Calendar. El payload completo contenía `required: []` en las herramientas sin parámetros; Interactions lo rechazó como JSON Schema inválido. El generador ahora omite `required` cuando no hay campos obligatorios. El payload productivo con las nueve herramientas fue repetido contra Gemini y respondió HTTP 200 solicitando `listar_reuniones` para la fecha actual.

Lista:

- [x] Reproducir la falla del proveedor sin crear reuniones ni enviar mensajes.
- [x] Verificar OAuth y lectura real de Google Calendar en PRD.
- [x] Confirmar que Gemini Interactions solicita `listar_reuniones` con la fecha actual correcta.
- [x] Migrar localmente el ciclo de herramientas a Interactions API en modo stateless (`store=false`).
- [x] Preservar los pasos y firmas del modelo, correlacionar `function_call` con `function_result` y soportar llamadas encadenadas.
- [x] Omitir arreglos `required` vacíos y validar contra Gemini el payload completo de las nueve herramientas.
- [x] Incluir el detalle seguro de errores Gemini en CloudWatch y responder un mensaje de contingencia antes de derivar por error técnico.
- [x] Permitir que solo una derivación `agent_error` se recupere automáticamente y vuelva a `bot` después de una respuesta exitosa; las derivaciones humanas siguen bloqueadas.
- [x] Cubrir el ciclo Gemini -> Calendar -> Gemini y la reactivación con pruebas locales.
- [x] Desplegar manualmente `api-inbox` en PRD.
- [x] Repetir el smoke real: pedir reunión para hoy, escoger horario, entregar nombre/correo y confirmar creación; el responsable confirmó que el agente ya puede agendar.
- [x] Revisar en Google Calendar el enlace Meet, asistentes, zona `America/Santiago`
  y ausencia de solapamiento del evento creado; lectura real aprobada el 27-10-2026.

## Lista P1: pendientes funcionales y operativos con el agente habilitado

- [x] Implementar `mark as read` a Meta después de procesar correctamente el mensaje y justo antes de enviar la respuesta; pendiente smoke tras despliegue.
- [x] Reponer el indicador de escritura usando la operación oficial combinada `status=read` + `typing_indicator.type=text`; pendiente smoke tras despliegue.
- [x] Persistir el estado `agent_processing` con lease de cuatro minutos, publicarlo por WebSocket y mostrarlo en la UI sin recargar el historial; pendiente despliegue y smoke.
- [x] Conservar datos parciales estructurados de reunión y contexto de cotización entre turnos con TTL de 30 días; pendiente smoke tras despliegue.
- [ ] Validar paridad de las operaciones restantes de Calendar con casos reales controlados: reagendamiento, confirmación de cancelación, feriados, zona horaria y prevención de choques.
- [x] Validar audios salientes end-to-end: AAC, audio MP4, OGG, MPEG y AMR quedaron
  persistidos con estado final `read`. La transcripción entrante sigue siendo un E2E
  distinto que requiere audios enviados por un teléfono real.
- [ ] Probar documentos, imágenes, audio y video entrantes; los tipos no soportados deben persistirse con una representación explícita y no ambigua.
- [ ] Confirmar límites de ventana de servicio de 24 horas y definir templates para cualquier envío que deba ocurrir fuera de ella.
- [x] Agregar notificación real a las alarmas DLQ; SNS `ALARM -> OK` y recepción humana
  fueron confirmados el 25-10-2026.
- [x] Agregar dashboard/alertas para webhook, antigüedad de colas, DLQ, realtime,
  Graph, Gemini, Calendar, derivación y latencia. La ampliación queda preparada
  localmente para el próximo despliegue de `api-inbox`.

## Lista P2: endurecimiento y operación

- [ ] Reducir permisos del módulo padre `COM` a lectura si no existe una razón funcional para CRUD.
- [x] Incorporar `businessAccountId`/`appId` como metadatos no secretos validados; el
  cambio queda preparado localmente y requiere desplegar API antes de actualizar SSM.
- [x] Definir rotación y prueba periódica de access token, app secret, verify token y
  cursor secret en el runbook operativo del 27-10-2026.
- [x] Centralizar Graph v26 como constante compartida para envíos y descargas.
- [ ] Definir recordatorio de actualización antes del retiro de Graph v26.
- [x] Evitar datos personales y contenido de mensajes en logs; se retiraron WAMID de
  logs operativos y una prueba automática bloquea identificadores y secretos directos.
- [x] Ejecutar prueba local reproducible de workers con 10.000 registros, fallos
  parciales controlados y presupuesto de cinco segundos; duplicados, desorden y
  progresión monotónica permanecen cubiertos por las pruebas de dominio.

## Secuencia posterior al corte

1. Guardar en Git la revisión exacta que ya está desplegada y revisar el diff completo antes del commit.
2. Desplegar primero `api-inbox` y luego la aplicación web para habilitar la reactivación explícita y `agent_processing`; comprobar persistencia parcial, lectura e indicadores de escritura/procesamiento.
3. Ejecutar los smokes restantes de Calendar, adjuntos y audio.
4. Conectar alarmas DLQ a un canal efectivo y agregar métricas operativas.
5. Mantener Node 24.15.0 y SSO `pa-prd` vigente como precondiciones de validación/despliegue.
6. Mantener rollback preparado: deshabilitar target y restaurar el callback legacy. No hay rollback de datos históricos porque la migración fue excluida.

## Comandos seguros ya ejecutados

El `dry-run` productivo utilizado fue:

```bash
go run ./cmd/migrate-inbox legacy-to-target dry-run \
  --source-profile root-pa \
  --source-region us-west-2 \
  --source-table conversations-prd-pri-usw2 \
  --target-profile pa-prd \
  --target-region us-east-1 \
  --target-table ind-prd-conversaciones-ddb-prd-pri-use1 \
  --manifest .migration/inbox-prd-manifest.json
```

La migración quedó fuera de alcance: no reemplazar `dry-run` por `apply` durante esta puesta en marcha.

## Validación actual

Con Node 24.15.0 finalizaron `go test -race`, `go vet`, `golangci-lint` sin hallazgos y la construcción ARM64 de las 26 funciones, incluida `fn-reactivar-agente-v1`. La release local exacta Serverless 4.6.2 resolvió correctamente la configuración PRD con SSO `pa-prd`. El launcher genérico `npx serverless` volvió a instalar 4.45.0 y `make validate` falla allí con `Build error: The service was stopped`; hasta fijar el launcher, el despliegue manual debe invocar directamente `~/.serverless/releases/4.6.2/package/dist/sf-core.js` y no usar `deploy-quick`.
