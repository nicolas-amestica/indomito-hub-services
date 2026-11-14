# Contexto de las derivaciones a atención humana

## Comportamiento

- El correo utiliza `libs/brandedemail`, la misma base visual de comprobantes y documentos tributarios. Adjunta al cuerpo los últimos diez mensajes del cliente y el agente IA anteriores a la derivación, con autor y fecha en America/Santiago. Si hay menos, incluye todos. Los mensajes humanos intermedios no cuentan dentro de esos diez.
- El historial se obtiene con `Query` paginada, independiente de `maxHistoryTurns`. Los mensajes largos se conservan completos; HTML se escapa. Los audios y archivos se identifican por nombre y se incluye la transcripción del mensaje actual cuando está disponible. Si falla la lectura, el correo advierte la falta de contexto y conserva el último mensaje.
- WhatsApp conserva la plantilla configurada y sus tres parámetros: contacto `wa.me`, detalle y motivo. El detalle incluye un enlace a Hub completo, aun si se necesita abreviar el texto del cliente. No agrega botones ni modifica el contenido fijo de la plantilla Meta.
- El enlace permite un único canje durante 24 horas desde su emisión. Una acción explícita “Ver conversación” hace el canje; cargar la página o su vista previa no consulta datos ni consume el permiso.
- El canje usa una transacción condicional y crea una sesión opaca de lectura de 30 minutos, independiente de la sesión normal. La lectura es privada y el authorizer valida su hash/vencimiento con una lectura consistente de DynamoDB en cada llamada, sin caché. Solo se guardan hashes en los registros de acceso; el enlace enviado también forma parte del mensaje saliente de WhatsApp. Quien tenga el enlace puede canjearlo antes que el destinatario. No identifica a un miembro particular del equipo.
- La sesión temporal vive en memoria del navegador. Al vencer, se borran los mensajes de la vista. Recargar/cerrar la página después del canje requiere ingresar con la cuenta habitual. El destino de la conversación se conserva durante el login.
- La vista muestra el historial por páginas de 50, con acceso a mensajes anteriores. No ofrece respuestas, descargas, edición ni acciones del agente. Los adjuntos aparecen como descripción; no se exponen URLs de descarga, claves S3, errores de entrega ni metadatos operacionales.
- Tokens de enlace viajan en el fragmento de URL, retirado al cargar la vista. El canje envía la credencial inicial en el cuerpo POST; las lecturas presentan la sesión opaca en `Authorization: Bearer`, sin adjuntar el JWT normal ni incluir el secreto en el cuerpo. Respuestas con `Cache-Control: no-store`; errores de canje desconocido, usado o vencido devuelven el mismo HTTP 410; una lectura sin credencial es rechazada por el Gateway con 401 y una sesión no autorizada con 403. Si el registro de dominio ya no existe o vence después de autorizar, la lectura puede responder 410. Nunca registrar cuerpos de estos endpoints en proxies o herramientas de observabilidad.
- La expiración se verifica en cada solicitud. DynamoDB TTL (`expiresAt`) solo limpia registros; no decide la validez. Se reutiliza la tabla Inbox sin nuevos recursos ni índices.

## Configuración y activación

Repos involucrados: backend Go, frontend Hub, authorizer e infraestructura SSM/API Gateway. La lectura `/derivaciones/lecturas` está protegida por el authorizer compartido y solo el canje inicial permanece público. El authorizer consume exclusivamente sesiones por hash, sin leer conversaciones ni añadir secretos de firma. Se retira la excepción anterior según `docs/standards/architecture/auth-flow.md` del orquestador.

El SecureString `/indomito/{stage}/inbox/derivation-notification-config` debe conservar `templateName`, `language`, `smtpParameterPath` y agregar:

| Ambiente | `hubBaseUrl` |
| --- | --- |
| DEV | `https://nuevo.admin.dev.girasindomito.cl` |
| PRD | `https://nuevo.admin.girasindomito.cl` |

Actualizar la fuente SSM de cada ambiente antes de desplegar el worker nuevo. Un parámetro sin `hubBaseUrl` es configuración incompleta. No imprimir los valores de parámetros que contienen credenciales. La infraestructura valida los cuatro campos y el origen HTTPS permitido.

Orden recomendado, primero DEV y luego PRD tras validación:

1. Aplicar el SecureString de derivación mediante el flujo SSM existente, conservando todos sus campos.
2. Publicar `iam-auth` con la nueva validación y su permiso `GetItem` limitado a sesiones. Publicar el módulo `api-gateway` para asegurar caché de autorización cero.
3. Publicar `api-inbox` (lectura privada) y después el frontend que presenta la sesión en Authorization. Publicar `payment-receipts` para activar también sus estilos compartidos. El bloque secuencial de comandos usa Node 24.15.0, el perfil del ambiente, verifica la cuenta destino y se detiene ante cualquier error.
4. Provocar una derivación controlada: confirmar correo y WhatsApp recibidos, diez mensajes originales y enlace navegable en el teléfono real. La aceptación de la API de Meta no acredita entrega.
5. Confirmar estado y definición actual de la plantilla en Meta. Se conservan las tres variables existentes, pero la aceptación y entrega del detalle con enlace deben validarse en el ambiente real. No se solicita automáticamente una plantilla nueva ni se cambia su categoría.
6. Abrir el enlace: no canjear antes de pulsar el botón, impedir segundo uso, leer páginas anteriores y comprobar acceso normal a esa conversación tras el login. Verificar expiración de la sesión y del enlace con permisos de prueba controlados.

Las pruebas locales no publican frontend, SSM ni Lambdas ni envían notificaciones reales.
