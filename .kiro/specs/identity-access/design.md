# Diseño

Tabla `usuarios` single-table:

- `LOGIN#<email|rut> / IDENTITY`: alias hacia el usuario.
- `USER#<id> / IDENTITY`: credenciales, estado y perfil.
- `USERS / USER#<id>`: proyección para listado administrativo.
- `MODULES / MODULE#<code>`: catálogo de módulos.
- `PROFILES / PROFILE#<code>`: catálogo de perfiles.
- `PROFILE#<code> / MODULE#<code>`: permisos del perfil.

El login resuelve el alias con GetItem, lee la identidad con GetItem, compara bcrypt, consulta permisos por la partición del perfil y firma un JWT. El authorizer verifica HS256 y publica `userId`, `email`, `rut` y `profileCode` en `requestContext.authorizer.lambda`.
