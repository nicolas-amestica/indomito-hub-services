# Auditoría local de seguridad y regresión monetaria

Fecha: 2026-10-05. No reemplaza pruebas de penetración ni evidencia AWS.

## Controles verificados

- Solo consulta RUT+código, webhooks verificados y retorno informativo son públicos. Las operaciones administrativas usan authorizer.
- RUT y código se convierten a claves HMAC; respuestas inválidas son genéricas y existen límites por IP y par normalizado.
- La sesión pública dura como máximo diez minutos, pertenece a una cuenta/gira y revalida la versión del código; rotar/revocar invalida sesiones.
- Checkout exige correo válido y monto del sistema. No acepta monto, cuenta ni cuota arbitrarios desde el navegador.
- Confirmaciones consultan al proveedor y validan receptor, moneda, monto e identidad. Retorno del navegador no contabiliza dinero.
- Pagos, referencias, comandos, diario, caja, recibo y outbox usan condiciones/transacciones idempotentes.
- Pago doble, respuesta perdida, cambios de nómina, descuento con intento abierto, reversa y fondos sin aplicar tienen regresiones concurrentes.
- PDFs están en S3 privado, cifrado, versionado, con bloqueo público y enlaces de 120 segundos.
- Fallos almacenan códigos controlados, no respuestas SMTP, secretos ni correos.
- IAM de pagos restringe claves líderes DynamoDB; no se encontró `Scan` ni secretos Khipu hardcodeados en los repositorios revisados.

## Corrección encontrada

El API compartido omitía los orígenes del portal. Se agregaron exactamente `http://localhost:4400` y `https://pagos.dev.girasindomito.cl`, conservando los orígenes administrativos. La suite de infraestructura valida los cuatro orígenes DEV.

## Riesgos pendientes de AWS

- Confirmar que el authorizer DEV desplegado entrega `paymentAccess=admin` solo a roles autorizados.
- Probar throttling real, WAF/abuso distribuido y ocultamiento de errores en API Gateway/CloudWatch.
- Verificar permisos efectivos con IAM Policy Simulator y CloudTrail, no solo CloudFormation local.
- Rotar las credenciales que hayan sido compartidas por canales inseguros y comprobar que solo existan en SSM SecureString.
- Ejecutar el recorrido DemoBank/Khipu, Streams, SQS, SMTP y S3 con datos ficticios.
