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
2. Las claves se almacenan exclusivamente como bcrypt. El secreto JWT se lee desde SSM SecureString.
3. Cada usuario tiene un perfil; cada perfil tiene varios módulos con permisos CRUD.
4. Todo endpoint excepto `POST /auth/login` requiere el authorizer compartido.
5. Favoritos obtiene `userId` exclusivamente del contexto del authorizer.
6. DynamoDB usa solo GetItem y Query con PK/SK; Scan e índices están prohibidos.
7. El perfil ADMIN inicial accede a Programas e IAM.
8. El frontend protege rutas, adjunta Bearer token, cierra sesión ante 401 y muestra únicamente módulos permitidos.
