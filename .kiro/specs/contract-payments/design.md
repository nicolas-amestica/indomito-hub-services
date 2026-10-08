# Diseño

## Límites

Contrato aprobado → evento durable → plan pendiente de puesta en marcha → nómina y calendario validados → publicación de cuentas. El contrato guarda la referencia y la política aplicable, no los pagos. Un anexo aprobado conserva su propia instantánea y efectos financieros.

El núcleo de cobros calcula transiciones sin I/O. La capa de aplicación confirma versiones y comandos con TransactWriteItems. Un comando incluye ID, actor, fecha, motivo y huella de entrada; reutilizar ID con otra entrada se rechaza. Ningún efecto externo se ejecuta dentro de un reintento automático de transacción.

## Accesos DynamoDB propuestos

| PK | SK | Acceso |
| --- | --- | --- |
| TRIP#id | META | Estado/versionado del plan y referencia contractual |
| TRIP#id | PARTICIPANT#id | Nómina paginada y asignación de liberados |
| ACCOUNT#id | META / INSTALLMENT#0001 | Cuenta y cuotas ordenadas |
| ACCOUNT#id | EVENT#ulid | Auditoría inmutable paginada |
| TRIP#id | ANNEX#id | Anexos y publicación de versión |
| CODE#HMAC(code) | META | Resolución de viaje, versión y revocación |
| TRIP#id | RUT#HMAC(rut) | Participación, sin índice global público |
| ATTEMPT#id | META | Intento con importe y revisión congelados |
| PROVIDER#name#reference | META | Dedupe y resolución de notificación |
| COMMAND#id | META | Idempotencia durable y resultado |
| RECEIPT#id | META | Referencia S3 privada y autorización |
| TAX_REQUEST#receiptId | META / ISSUANCE#MANUAL | Solicitud tributaria e ingreso manual inmutable |
| TAX_QUEUE#PENDING | fecha#requestId | Bandeja de boletas pendientes, paginada por partición |
| TAX_QUEUE#RECORDED | fecha#requestId | Historial operativo de boletas registradas |
| TAX_FOLIO#tipo#folio | META | Reserva única de folio por tipo documental |
| MONTH#yyyy-mm | TRIP#id | Resúmenes y alertas paginadas |
| JOB#bucket | PENDING#ulid | Recuperación acotada de trabajo pendiente |
| BANK#id#yyyy-mm | MOVEMENT#date#id | Conciliación y caja efectiva |
| CONTRACT#id | SIGNED_UPLOAD#id | Intención idempotente de carga directa y expectativa de integridad |
| CONTRACT#id | SIGNED_DOCUMENT#fecha#id | Historial inmutable de copias firmadas |

No TTL en información financiera. TTL solo para sesiones y límites temporales. No usar el borrado eventual TTL como liberación de bloqueo.

## Invariantes

- Original = descuentos + cancelaciones + cobros aplicados + saldo pendiente.
- Cobros recibidos = aplicados + fondos no aplicados; una devolución tiene su propia salida, no borra el ingreso.
- Aprobado a devolver = devuelto + pendiente de devolver.
- Cada asiento tiene débitos y créditos iguales; una proyección no constituye la fuente histórica.
- Una baja no recalcula ninguna otra cuenta.
- Dinero recibido, deuda extinguida y efectivo bancario no son sinónimos.

## Implementación incremental actual

El núcleo `domain/collection` y la persistencia `functions/collection` están aislados del simulador y de las pruebas técnicas Khipu. La puesta en marcha está conectada a dos endpoints administrativos y una pantalla protegida. El recorrido público y los cobros aún no están habilitados.

La primera persistencia guarda una cuenta acotada a 120 cuotas en META, sin historial embebido. Serializa cambios por cuenta para impedir pagos y descuentos concurrentes. Los eventos son registros separados. El comando conserva la respuesta acotada para reintentos deterministas; esto añade escrituras proporcionales al tamaño de la cuenta. Antes de habilitar tráfico debe medirse el tamaño real y decidir si extraer obligaciones individuales reduce el costo total. No afirmar que ProjectionExpression reduce RCUs.

PreparePlan escribe una cuenta y el contador del plan por transacción; un plan PREPARING permanece invisible hasta publicarse ACTIVE. El adaptador administrativo lee el contrato aprobado por clave y solo acepta la selección de liberados, no importes, fechas ni el booleano Approved enviados por el navegador.

La aprobación contractual en DEV agrega, en la misma transacción del contrato y auditoría, la reserva única `CODE#HMAC/META` y el evento inmutable `TRIP#id/APPROVAL`, con versión contractual, huella PDF y estado PENDING_SETUP. Este evento no es el META mutable del plan. El código se genera con crypto/rand (32 símbolos, seis caracteres) y solo se conserva en claro en las instrucciones del contrato/PDF administrativo. El índice usa HMAC-SHA256 con prefijo `trip-code:v1:` y clave SSM independiente. La generación se realiza antes del PDF; la transacción condicional impide publicar un código duplicado. Si hay colisión se rechaza la aprobación y un nuevo intento genera otro código.

S3 se escribe antes de la transacción: una operación fallida puede dejar un PDF privado huérfano, pero nunca una aprobación parcial ni sobrescribir el PDF ganador porque su clave incorpora SHA256. No enviar el PDF ni activar el portal hasta que la transacción esté confirmada. Si se pierde la respuesta después del commit, se recupera únicamente la aprobación con la misma versión y huella PDF. La reserva del código no permite por sí sola consultar pasajeros: el futuro resolvedor público deberá exigir plan ACTIVE, código vigente, RUT y límites de intentos.

Este registro durable por clave no constituye todavía un outbox con trabajador ni un listado de grupos pendientes. Ambos accesos deben implementarse y probarse antes del recorrido completo. Las condiciones de acceso históricas no se regeneran; la cláusula v2 de nómina exige anexos y aclara que el portal de pagos no modifica pasajeros.

Las cláusulas nuevas usan refundPolicyVersion=2; la versión histórica permanece legible. La API aplica la política vigente a documentos no aprobados y exige calendario completo. Los PDFs aprobados existentes no se regeneran. Los nuevos objetos PDF incorporan la huella en su clave para que una aprobación concurrente perdedora no sobrescriba el documento ganador.

La firma manuscrita tiene un ciclo separado. Al aprobar, `signatureStatus=PENDING_SIGNED_UPLOAD`; contratos aprobados históricos sin atributo se interpretan igual. Preparar una carga crea por clave un intento idempotente con tamaño, SHA-256, versión aprobada y documento activo observado, y entrega un PUT S3 prefirmado. Finalizar relee el objeto, valida límite, tipo y cabecera `%PDF-`, verifica tamaño/huella y publica en una transacción el registro inmutable más el puntero activo del contrato. La condición sobre el documento activo observado hace que una sola carga concurrente gane. Un reemplazo exige motivo, conserva versiones anteriores y nunca incrementa la versión contractual ni altera cobranza.

Las claves usan `contracts/{contractId}/signed/{documentId}.pdf` en el bucket privado existente. Las descargas son URLs GET breves. El listado por contrato consulta su propia partición y el listado general proyecta `signatureStatus`. DynamoDB no permite ampliar la proyección del GSI desplegado: la migración crea `gsi-periodo-documental-index`, cambia ambos lectores y luego elimina `gsi-periodo-resumen-index`. Los dos índices coexisten solo entre esos despliegues; el estado final conserva uno. No hay Scan. La interfaz aclara que la confirmación humana comprueba correspondencia y presencia de firmas, no autenticidad criptográfica.

La redacción de devolución es una política comercial con salvaguarda de derechos irrenunciables, no una certificación legal. Revisar con asesoría jurídica antes de su uso contractual definitivo; referencia: https://www.sernac.cl/604/w3-article-88402.html.

## Experiencia de pago, comprobante y operación tributaria manual

El clic en pagar abre inmediatamente una pestaña transitoria desde el gesto del usuario; la pestaña queda sin `opener`, espera la creación del checkout y navega únicamente a una URL Khipu validada. Si el navegador bloquea la pestaña se ofrece un control de recuperación, no un segundo paso habitual. El retorno y la cancelación vuelven al portal; jamás confirman dinero. Al recuperar foco, el portal consulta el intento y refresca la cuenta por la sesión acotada.

Los comprobantes nuevos usan documento v3. Su QR contiene la ruta pública y el código estable `receiptId`; la verificación hace un `GetItem` directo y responde solo autenticidad, monto, fecha, concepto, estado y versión, sin RUT, nombre, correo ni URL S3. Los v1/v2 siguen legibles y el backfill explícito por cuenta genera v3 sin reenviar correos ni alterar eventos o saldos.

Una confirmación Khipu y su solicitud tributaria se escriben en la misma transacción financiera. La bandeja administrativa permite emitir manualmente una boleta de ventas electrónica en SII y luego registrar folio, fecha y PDF; el navegador no envía monto. `MANUAL_RECORDED` significa solo que el operador incorporó el documento, no que el sistema verificó aceptación SII. La futura automatización consumirá la solicitud v2 mediante un adaptador tributario sin permisos de escritura sobre cuotas, caja o comprobantes.

El código vigente de acceso se lee por claves conocidas desde la aprobación o el contrato aprobado y se expone únicamente a administración. No se lista, registra en logs ni devuelve por endpoints públicos. Contratos nuevos guardan también el valor administrativo en la aprobación; los existentes se resuelven por `GetItem` directo, sin migración masiva ni GSI.

## Separación de frontends

`app-ngx-hub` conserva el repositorio físico `ind-hub-app-ngx-pri-gh` y contiene exclusivamente la SPA administrativa autenticada. `app-ngx-pay` vive en `ind-pay-app-ngx-pri-gh` y contiene exclusivamente el portal público. Cada repositorio tiene un único `main.ts`, `angular.json`, `package.json`, conjunto de pruebas y script de despliegue; no existe un build alternativo que compile el otro frontend.

La separación es física, no un paquete compartido en tiempo de ejecución. El preset PrimeNG, los tokens visuales y los helpers mínimos se copiaron al nuevo repositorio al crear la frontera para conservar el mismo stack; desde ese punto cada cambio transversal debe coordinarse y validarse en ambos. Esto evita que una actualización o dependencia administrativa aumente el bundle o la superficie pública. No se crea una librería común ni se modifica el backend en esta etapa.

En DEV, `app-ngx-hub` continúa publicando administración y `app-ngx-pay` publica `pagos.dev.girasindomito.cl` en el bucket/CDN existente de pagos. El portal se ejecuta localmente en el puerto 4400. Un despliegue del Hub nunca sincroniza el bucket de pagos y viceversa. La futura habilitación productiva del portal exige su propia configuración de infraestructura y no se infiere por disponer de `environment.production.ts`.

El orquestador usa únicamente los aliases `app-ngx-hub` y `app-ngx-pay`, distribuye contexto Angular a ambos y mantiene rutas/scopes diferenciados. Los contratos HTTP, sesiones públicas acotadas, CORS y servicios Go permanecen sin cambios.

## Catálogo de servicios y navegación administrativa

El catálogo conserva las filas existentes bajo `CAT#SRV#<catalogId>` y agrega una relación directa `CAT#SRV#SCOPE#<scope>/META -> catalogPk`. Listar hace un `GetItem` de la relación y un `Query` de la partición, por lo que soporta el ULID físico actual sin Scan, GSI ni valor hardcodeado en Angular. La migración DEV crea únicamente la relación para `CTZ`; las altas posteriores reutilizan esa colección. El formulario solicita solo activos; Administración puede incluir inactivos y modificar `glosa`, `price`, `currency`, `chargeType`, `active` y `default` con condición de existencia.

El frontend mantiene el catálogo en un store feature-scoped. El autocompletado PrimeNG filtra desde memoria después de dos caracteres y conserva entrada libre. Una selección proyecta los cuatro campos editables de la fila. La precarga reemplaza la fila vacía únicamente si la lista sigue pristine; el reset explícito vuelve a construir los defaults. Una respuesta tardía nunca pisa texto ya escrito. IAM agrega el GET al módulo `PROGRAMS` para quienes crean cotizaciones; `SERVICE_CATALOG` bajo `ADM` conserva creación, edición y lectura de inactivos sólo para administración.

El menú adopta el patrón estructural de Axity —drawer centrado, cabecera, buscador, filtros por categoría y filas compactas— adaptado a los módulos IAM de Indómito Hub. Se conserva PrimeNG para drawer, botones e inputs, Tailwind para layout y los guards existentes como frontera efectiva; ocultar una opción nunca sustituye autorización.

## Composición común de comprobantes verificables

El trabajador Go conserva un solo renderer para cuotas, abonos individuales, fondos en revisión y abonos grupales. La versión documental vigente activa un helper común que construye la URL, genera el QR y dibuja QR/código; cada tipo solo aporta identidad y concepto. Los nuevos abonos grupales pasan a v3. Los v1/v2 históricos siguen descargables y pueden migrarse mediante backfill explícito, sin regeneración automática ni correos duplicados.

## Seguridad y operaciones pendientes

Authorizer compartido valida JWT público con audience/tipo y alcance de una participación, distinto del administrativo. Entrada pública RUT+código limita intentos antes de revelar existencia; nunca incluir secretos en URL/logs. Khipu mantiene DEMO BANK en DEV y validación autoritativa de receptor, moneda, referencia y monto. Confirmación discrepante se registra como fondos por resolver.

S3 privado para comprobantes con enlaces firmados de corta duración. Outbox más trabajador Go idempotente genera PDF y correo SMTP/TLS, con reintentos limitados y cola de fallos. Reenvío no altera documento original. Separar direcciones de notificación de identidad autenticada.

Operaciones grandes: generar versión pendiente por lotes con claves deterministas, verificar cantidad/huella, publicar versión mediante condición sobre META. No exponer registros parciales ni usar una transacción por viaje completo.

Despliegue: pruebas de dominio → persistencia con concurrencia → integración simulada → authorizer/infra/API/frontend DEV → prueba de banco de demostración supervisada. No migrar contratos existentes sin revisión explícita de condiciones y saldos de apertura.
