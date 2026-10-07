# Operación del código de acceso al portal de pagos

## Propósito

Cada gira usa RUT del pasajero más un código de seis caracteres. El código visible nunca se usa como clave DynamoDB: el portal consulta una clave HMAC. La rotación y revocación son operaciones administrativas y no reescriben el contrato ya aprobado.

## Rotar

1. Abrir **Contrato > Acceso portal**.
2. Verificar la gira y la versión vigente.
3. Escribir un motivo auditado, confirmar el impacto y seleccionar **Rotar código**.
4. Copiar el nuevo código mostrado y comunicarlo por un canal autorizado.

La transacción revoca el registro HMAC anterior, crea el nuevo y avanza la versión. Como cada solicitud pública revalida el registro del código, también quedan inválidas las sesiones emitidas con la versión anterior. Una respuesta perdida se comprueba recargando la pantalla; el comando conserva el mismo resultado y no genera otro código.

## Revocar

La revocación invalida el código y todas sus sesiones sin reemplazarlo. Es irreversible desde esta pantalla: para reactivar una gira revocada se requiere un procedimiento excepcional aún no habilitado, con revisión de seguridad y auditoría.

## Alcances y resguardos

- El código original permanece solamente en el contrato/PDF. Antes de la primera rotación, la pantalla muestra esa condición sin intentar recuperarlo desde DynamoDB.
- El código rotado se conserva en la vista administrativa y en el comando idempotente para poder recuperar una respuesta perdida; el endpoint público trabaja solo con HMAC.
- No registrar códigos, RUT ni tokens en logs, tickets o este documento.
- Ante un resultado incierto, recargar y consultar el estado durable antes de repetir una acción.
