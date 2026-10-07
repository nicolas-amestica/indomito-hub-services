# Puesta en marcha: integración y límites

## Nuevos parámetros de aprobación (antes de desplegar api-contract en DEV)

Agregar en `infra/environments/.env.dev`, sin versionar valores secretos:

- `SSM_PAYMENTS_PORTAL_URL=https://pagos.dev.girasindomito.cl` → `/indomito/dev/payments/portal-url`.
- `SSM_PAYMENTS_LOOKUP_SECRET`: secreto aleatorio independiente de al menos 32 bytes (por ejemplo, 64 caracteres hexadecimales) → `/indomito/dev/payments/lookup-secret`.

Ambos están registrados como SecureString `secureOnly`; ejecutar `make deploy resource=ssm stage=dev region=us-east-1` para aplicar el paso seguro existente. Nunca copiar la clave Khipu ni JWT para este propósito. No rotar esta clave aisladamente: cambiaría las claves de búsqueda de los códigos ya emitidos; se necesita migración/versionado explícito.

La Lambda de actualización de contratos lee estos parámetros con descifrado en tiempo de ejecución. Si no están configurados, la aprobación DEV falla cerrada sin aprobar el contrato. Las otras operaciones no consultan los secretos. El despliegue PRD no habilita todavía esta integración.

La pantalla administrativa está en `/contratos/:id/puesta-en-marcha`. El contrato aprobado muestra un enlace solo para usuarios con el módulo `PAYMENT_SETUP`. La lectura de la pantalla no crea cuentas.

## Permisos antes de habilitar DEV

Sesiones del portal: agregar `SSM_AUTH_PAYMENT_SESSION_SECRET` a `infra/environments/.env.dev` con un secreto aleatorio independiente de al menos 32 bytes (por ejemplo, 64 caracteres hexadecimales). Se registra como SecureString secureOnly en `/indomito/dev/auth/payment-session-secret`. No reutilizar claves Khipu, JWT administrativo ni lookup. La configuración ya está registrada, pero no se creó ni desplegó un valor secreto durante esta implementación.

El authorizer reserva `/pagos/portal` para sesiones de pasajero, sin fallback administrativo/legacy. Solo contempla POST `/pagos/portal/checkout` y GET `/pagos/portal/intentos/{ULID}`. El JWT exige HS256, issuer `indomito-payments-dev`, audience `indomito-payment-portal-dev`, tokenUse `passenger-payment`, `sub`=ULID de cuenta, `tripId`, `jti`, `codeKey` HMAC, iat/exp y duración máxima 600 segundos. No incluye RUT ni código claro. Contexto: paymentAccess=passenger, paymentAccountId, paymentTripId, paymentSessionId, paymentCodeKey. La clave no se agrega a los secretos administrativos. La configuración actual del Gateway desactiva cache de autorización (TTL=0); preservar esta condición para respetar expiración.

La consulta exitosa ya emite la sesión en `data.session.accessToken` y su vencimiento Unix en `data.session.expiresAt`. El bootstrap público exige los dos SecureString (lookup y sesión), rechaza placeholders y claves iguales y refresca cada cinco minutos. Si falta la configuración, falla cerrada; no sustituye la clave por una administrativa. No registra sesiones en DynamoDB ni agrega índices. El JWT está firmado, no cifrado: contiene ULID de cuenta/viaje/sesión y clave HMAC del código, pero no RUT, correo ni código claro. El frontend deberá conservarlo solo en memoria y nunca incluirlo en URL o logs.

Los handlers de checkout y lectura de intento usan `PassengerAccount`: comprueba contexto passenger del authorizer, código vigente, pertenencia al viaje y plan ACTIVE mediante claves directas. Verifican además que el intento pertenezca a la cuenta autorizada. Pruebas rechazan códigos revocados, viajes cruzados, planes incompletos y contexto falsificado en el cuerpo. La revocación del código se comprueba en cada solicitud, no solo al verificar el JWT.

### API del checkout DEV (implementada, no desplegada)

- `POST /pagos/portal/checkout`: Bearer de sesión pública, cabecera `Idempotency-Key` ULID y cuerpo exclusivo `{email}`. No admite importe, cuota ni accountId. Reserva la primera cuota pendiente completa y usa DemoBank. Una clave repetida no cambia el correo ni inicia la cuota siguiente; el ID interno se deriva de cuenta+clave para aislar pasajeros.
- `GET /pagos/portal/intentos/{id}`: requiere sesión de la cuenta propietaria; solo lee DynamoDB, sin credenciales Khipu. Devuelve ID, estado y enlace vigente cuando corresponde, nunca correo ni referencias financieras.
- Estados públicos: PENDING_PAYMENT, CONFIRMED, REVIEW_REQUIRED, RECONCILIATION_REQUIRED. Ante creación ambigua se responde 202 con el ID para consultar, sin repetir el cobro. Un intento vencido no desbloquea la cuenta.

El checkout usa como notify URL `/pagos/cuotas/khipu/notificaciones`. Retorno y cancelación siguen mostrando la página de verificación existente, no confirman pagos. Si el pago fue confirmado pero se perdió por completo el resultado de creación (no existe CHECKOUT), esta primera consulta pública conserva RECONCILIATION_REQUIRED; falta publicar una proyección por intento que permita mostrar esa recuperación. No confundir ese estado conservador con pérdida del ingreso: el diario y la deduplicación ya lo conservan. Las pruebas integradas AWS siguen pendientes; `checkoutEnabled` permanece false.

El frontend ya integra creación y consulta de intento detrás de esa bandera. Exige correo válido, conserva la misma referencia y correo ante una respuesta perdida y bloquea envíos concurrentes. Mantiene la sesión y el intento exclusivamente en memoria; nunca en URL ni almacenamiento web. El enlace se muestra únicamente en estado pendiente y para hosts Khipu permitidos. Abrirlo no modifica las cuotas: la confirmación se consulta al servidor y luego se vuelve a consultar la cuenta. Los estados de revisión y conciliación advierten que no se debe repetir el pago. Verificado con 366 pruebas frontend y compilación payments DEV, sin llamadas reales de cobro.

Al recargar, cerrar la consulta o vencer la sesión, el cliente vuelve a ingresar RUT+código. La consulta devuelve `openAttemptId` desde la cuenta ya leída, sin lecturas adicionales ni índices. Angular bloquea la creación y consulta ese intento con la nueva sesión; un error conserva el bloqueo y permite reintentar la lectura. También muestra el estado de un intento abierto para pasajeros inactivos o con checkout deshabilitado. No recupera ni expone el correo original. Pruebas: 369 frontend y build payments DEV, además de Go race/vet/lint.

Las confirmaciones excepcionales conservan `ReviewAttemptID` en la misma cuenta/transacción financiera, aun después de cerrar `OpenAttemptID`. El lookup devuelve `reviewRequired` y la primera referencia de revisión, sin importes internos ni datos bancarios. Angular prioriza recuperar esa referencia y presenta una advertencia persistente. El dominio rechaza nuevos intentos, el API no reutiliza enlaces pendientes mientras exista revisión y el frontend tampoco los muestra. Los ingresos tardíos auténticos siguen registrándose, sin perder fondos ni sobrescribir la primera referencia; el historial permanece en el diario.

Protección conservadora para datos anteriores: `UnappliedReceived > ReviewedUnapplied` bloquea aunque no exista referencia recuperable. Son acumulados; aprobar o pagar una devolución NO libera automáticamente. La operación administrativa `RESOLVE_REFUNDED_REVIEW` exige haber aprobado todos los fondos no aplicados y pagado todas las devoluciones aprobadas, sin intento abierto. Conserva acumulados y genera auditoría sin movimiento de caja. No implementa todavía reasignaciones a cuotas ni resoluciones parciales.

La proyección `ATTEMPT#/OUTCOME` ya permite recuperar confirmaciones sin CHECKOUT. Comprobantes, descargas y reenvíos están implementados localmente; ver [operación de comprobantes](receipt-operations.md). Siguen pendientes reasignaciones, reversas, conciliación persistente, validación integrada AWS y los demás puntos de tasks.md. El deploy queda exclusivamente a cargo del usuario.

- Registrar `PAYMENT_SETUP` en el catálogo IAM siguiendo el esquema de módulos existente y asignarlo únicamente al personal de cobranza autorizado. No se crean permisos de usuarios automáticamente desde el código.
- Dar a ese rol acceso API explícito a `/pagos/contratos`: `r` para consultar y `c` para confirmar. El authorizer de pagos exige JWT firmado, vigente, usuario y acceso por segmento; publica `paymentAccess=admin` en el contexto.
- El guard Angular no sustituye la autorización del backend. Tener permisos para editar contratos no otorga automáticamente permisos financieros.
- Desplegar los cambios del authorizer y las dos funciones de api-payment antes de habilitar el acceso al frontend. No ampliar permisos a todo `/pagos` por conveniencia.

## Contrato de API

`GET /pagos/contratos/{id}/puesta-en-marcha` lee el contrato aprobado por clave y el estado del plan. Devuelve condiciones, nómina administrativa, vencimientos y selección anterior si existe. Rechaza contratos históricos sin política v2/calendario completo; requiere un proceso explícito de regularización, no inventa fechas.

`POST` solo acepta `contractVersion` y `freeParticipantIds`. Importes y fechas se leen del contrato, nunca del navegador. Repetir la misma selección es idempotente. Otra selección tras iniciar la preparación se rechaza.

La instantánea `TRIP#{id}/SETUP` se guarda una sola vez junto con `META` inicial. Se mantiene separada para no reescribir toda la nómina al incrementar el contador de preparación de cada cuenta. `META` controla PREPARING/ACTIVE. Solo ACTIVE permite acceder a cuentas. Una interrupción se reanuda con las mismas condiciones; no se borran preparaciones incompletas por TTL.

## Lo que esta etapa NO hace

No registra el abono como cobrado, no cobra por Khipu, no habilita por sí sola el portal público, no emite comprobantes y no ejecuta devoluciones. La UI no debe presentarse como recorrido de pago completo. El código de viaje ya se genera al aprobar; quedan pendientes anexos, conciliación/recibos y pruebas integradas en AWS.

La persistencia tiene pruebas transaccionales simuladas; no sustituyen una prueba con DynamoDB real. La puesta en marcha es síncrona y reanudable: medir duración/costo para nóminas grandes antes de habilitarlas, y agregar procesamiento asíncrono si supera el tiempo del API.

## Consulta pública implementada, todavía sin despliegue integrado

### Webhook de cuotas (implementado, pendiente de desplegar)

`POST /pagos/cuotas/khipu/notificaciones` es exclusivo de cuotas. No reemplaza `/pagos/khipu/notificaciones`, que conserva el flujo técnico de pruebas. El futuro handler de creación debe usar la nueva ruta como `NotifyURL`; no se cambia automáticamente una URL de cobro ya creado.

Verifica firma y vigencia sobre bytes originales (incluido transporte base64 de API Gateway) antes de consultar cuentas. Luego valida el pago contra Khipu y ejecuta la confirmación transaccional. Solo retorna 200 tras persistencia o duplicado ya confirmado; errores de proveedor/almacenamiento no se reconocen como procesados. No devuelve cuentas ni detalles internos. Pruebas locales cubren firma inválida, alteración, fecha vencida, tamaño, base64, IDs y repetición. Falta validar reintentos reales y permisos en AWS.

Usa los tres parámetros Khipu DEV existentes (`api-key`, `webhook-secret`, `receiver-id`), refrescados cada cinco minutos; no requiere nuevas variables SSM. El binario incluye reglas de zona America/Santiago. IAM permite GetItem solo en familias ATTEMPT/ACCOUNT/TRIP/COMMAND/REFERENCE y PutItem en ACCOUNT/COMMAND/REFERENCE/RECEIPT/JOB; no permite modificar contratos, códigos o intentos. No agrega tablas ni índices; cada notificación válida utiliza la transacción financiera existente. La firma no sustituye protección de borde contra tráfico abusivo.

### Garantía interna previa a checkout

`ReserveCheckout` congela cuota, importe y correo, sin registrar caja. Recuperar esta reserva NO autoriza repetir un POST al proveedor. `ClaimCheckoutDispatch` crea un registro permanente `ATTEMPT#/DISPATCH` condicionado a su inexistencia y revalida la versión de la cuenta en la misma transacción (dos escrituras, sin nuevos índices). Solo la ejecución que recibe éxito puede enviar una vez. Un error o respuesta perdida no concede permiso, aunque la escritura haya ocurrido. No hay desbloqueo por tiempo ni TTL.

Esto privilegia evitar duplicados sobre disponibilidad: una interrupción después de obtener el permiso y antes de llamar al proveedor requiere conciliación aunque no haya cobro externo. Falta recuperación operativa; la sesión pública está implementada, pero no hay todavía un checkout habilitado para clientes. La reserva original permanece inmutable y la autorización de envío tiene auditoría propia, sin afectar importes pagados ni caja.

`CreateDevelopmentCheckout` conecta internamente la reserva con el adaptador existente: verifica DemoBank antes de reclamar el envío y toma importe e identidad del intento persistido. No acepta el importe desde el navegador. Guarda `ATTEMPT#/CHECKOUT` con estado PENDING_PAYMENT, URL y vencimiento; no lo confunde con pago recibido. Recupera una respuesta perdida de esta escritura solo si el resultado coincide exactamente. Solicitudes posteriores reutilizan el enlace vigente sin otro POST; si venció o hubo un error ambiguo, exigen conciliación sin desbloquear la cuenta. Las URL de retorno/notificación pertenecen a configuración del servidor. Sigue pendiente publicar el handler autorizado y conectar la confirmación con el diario financiero.

Referencia: [flujo oficial Khipu](https://docs.khipu.com/payment-solutions/instant-payments/description). La creación y el retorno del navegador no demuestran recepción del dinero. Las pruebas de este servicio usan un adaptador simulado; no sustituyen pruebas integradas con DynamoDB y la cuenta desarrollador.

`ConfirmProviderCheckout` verifica contra el adaptador la identidad del intento, el pago, el cobrador configurado, moneda e importe antes de contabilizar. El comando/comprobante tiene un ULID determinista derivado de la referencia Khipu (no usar su componente temporal para ordenar); la referencia global impide duplicar el ingreso. Cuenta, diario, deduplicación y outbox se escriben juntos. Fecha efectiva usa la zona de negocio explícita y fecha de registro conserva el momento de procesamiento.

La contrapartida es PROVIDER_RECEIVABLE, no BANK: todavía falta conciliar la liquidación. Si el pasajero está inactivo, su cuota ya no admite aplicación o el importe auténtico difiere del reservado, el ingreso completo queda en UNAPPLIED_FUNDS para revisión. No se aplica parcialmente ni se reparte automáticamente entre cuotas. Esto no aprueba ni ejecuta una devolución automáticamente. Comprobante y correo continúan pendientes del trabajador. El webhook desplegado de pruebas todavía no llama esta función. Reversas y otros estados excepcionales requieren ampliar la conciliación antes de habilitar el recorrido público y no se deben presentar como resueltos.

El nuevo método del adaptador `VerifyReceived` valida identidad, cobrador, CLP entero positivo y estado confirmado normal, devolviendo el importe real. `Verify` conserva su exigencia de importe exacto para el flujo técnico anterior. Fracciones no nulas, formatos exponenciales, valores negativos/cero y sobre el máximo operativo se rechazan; no se redondean. La revisión administrativa de importes discrepantes y su eventual devolución continúan pendientes.

`POST /pagos/consultas` recibe `{rut, tripCode}` en el cuerpo, nunca en la URL. La pantalla principal del portal usa este endpoint real de DEV, no el simulador local anterior. Ver la pantalla en localhost no demuestra que el endpoint esté desplegado.

La búsqueda valida el dígito verificador y normaliza el RUT. Lee por clave el código HMAC, la relación `TRIP#/RUT#`, la cuenta y el estado ACTIVE del plan. La puesta en marcha crea cada relación junto con la cuenta en una transacción de hasta tres escrituras. No se agregan GSI ni Scan. Los documentos extranjeros siguen disponibles administrativamente, pero no obtienen acceso público por RUT. Preparaciones anteriores sin estas relaciones requieren una migración explícita; no se modifica su instantánea automáticamente.

La vista de cuotas contiene estado activo/liberado y número, vencimiento, importe, pagado, pendiente y estado de las cuotas, acompañada de la sesión descrita arriba. No devuelve nombre, RUT, motivos administrativos ni referencias bancarias. Un saldo extinguido por descuento no se presenta como dinero pagado. Checkout visible permanece deshabilitado hasta completar recuperación, comprobantes y verificación DEV.

Límites actuales por ventana fija de un minuto: diez consultas por IP y cinco por combinación RUT/código. IPv6 se agrupa por /64. Los contadores usan CAS transaccional y TTL exclusivamente temporal. Una consulta permitida consume hasta dos lecturas de contador, dos escrituras transaccionales y cuatro lecturas de búsqueda; las colisiones pueden requerir reintentos acotados. No equivale a costo cero ni a protección DDoS: falta verificar throttling/protección de borde y medir consumo real antes de publicar. Cambiar de ventana puede permitir ráfagas adicionales.

La Lambda pública solo tiene lectura por familias de claves autorizadas y escritura en `RATE#`; no tiene permisos de modificación financiera ni secretos Khipu. Debe comprobarse esta política contra AWS real antes de habilitarla. La revocación de códigos se respeta en lectura, pero su operación administrativa y rotación siguen pendientes.
