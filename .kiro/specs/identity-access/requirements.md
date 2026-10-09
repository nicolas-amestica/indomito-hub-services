# Autenticación y control de acceso

## Repos involucrados

| Repo | Responsabilidad |
|---|---|
| services | Login, permisos y administración IAM |
| authorizer | Validación JWT y contexto de identidad |
| infrastructure | Tabla usuarios existente y Gateway |
| application | Login, sesión, guards, menú y panel IAM |
| orchestrator | Contratos OpenAPI y arquitectura |

## Requisitos

1. El login acepta RUT chileno o correo y una clave, devuelve un JWT HS256 de vida limitada y nunca expone el hash.
2. Las claves nuevas se almacenan con Argon2id. Los hashes bcrypt heredados solo se conservan hasta el siguiente login correcto, cuando se migran. El secreto JWT se lee desde SSM SecureString.
3. Cada usuario tiene un perfil; cada perfil tiene varios módulos con permisos CRUD.
4. Todo endpoint excepto login, solicitud de recuperación y restablecimiento de clave requiere el authorizer compartido.
5. Favoritos obtiene `userId` exclusivamente del contexto del authorizer.
6. DynamoDB usa solo GetItem y Query con PK/SK; Scan e índices están prohibidos.
7. El perfil ADMIN inicial accede a Programas e IAM.
8. El frontend protege rutas, adjunta Bearer token, cierra sesión ante 401 y muestra únicamente módulos permitidos.
9. La recuperación responde sin revelar si el correo existe, envía un enlace por SMTP/TLS, conserva solo el hash del token, expira a los 20 minutos, limita intentos y permite un único uso.
