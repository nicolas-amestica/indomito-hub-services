# Diseño

Tabla `usuarios` single-table:

- `LOGIN#<email|rut> / IDENTITY`: alias hacia el usuario.
- `USER#<id> / IDENTITY`: credenciales, estado y perfil.
- `USERS / USER#<id>`: proyección para listado administrativo.
- `MODULES / MODULE#<code>`: catálogo de módulos.
- `PROFILES / PROFILE#<code>`: catálogo de perfiles.
- `PROFILE#<code> / MODULE#<code>`: permisos del perfil.

El login resuelve el alias con GetItem, lee la identidad con GetItem, verifica Argon2id o bcrypt heredado, consulta permisos por la partición del perfil y firma un JWT. Después de un login bcrypt correcto migra el hash de forma condicional a Argon2id. El authorizer verifica HS256 y publica `userId`, `email`, `rut` y `profileCode` en `requestContext.authorizer.lambda`.

La recuperación guarda en la identidad únicamente SHA-256 del token aleatorio,
expiración y contador de intentos. El correo obtiene la URL desde una
configuración SMTP/TLS en SSM SecureString. Una solicitud posterior reemplaza
el hash anterior y el restablecimiento elimina todos los atributos temporales.
