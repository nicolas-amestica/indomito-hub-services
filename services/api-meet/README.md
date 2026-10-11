# Agenda de Videollamadas — api-meet

Servicio Go para Google Calendar (GET/POST/PUT/DELETE) y Google Meet (creación de espacios).

## Seguridad y despliegue

- Solo se permite stage `dev` en `serverless.ts`. Los cuatro endpoints se registran como privados en el authorizer del HTTP API compartido.
- No guardar los valores OAuth en Git. AWS SSM debe contener `/indomito/dev/google/calendar-oauth` y `/indomito/dev/google/meet-host-oauth` (SecureString) y `/indomito/dev/google/calendar-id` (String).
- El token Calendar necesita autorización `https://www.googleapis.com/auth/calendar.events` (o `.../calendar`) y permisos de edición en **el calendario configurado**.
- El token de la cuenta anfitriona Meet necesita `https://www.googleapis.com/auth/meetings.space.created`.
- No ejecutar escrituras de prueba sobre el calendario real: crear un calendario y credenciales OAuth de prueba, configurar los parámetros DEV para ese calendario **solo durante pruebas autorizadas**.
- El registro del módulo se prepara con `scripts/configure-meet-iam-dev.sh`; no se ejecuta automáticamente al crear el PR.

## Comprobaciones locales

```bash
npm ci
cd services/api-meet && go mod tidy -diff && go test -race -count=1 ./... && go vet ./...
cd ../..
make build service=services/api-meet
npx tsc --noEmit --target ES2022 --module NodeNext --moduleResolution NodeNext --strict --skipLibCheck services/api-meet/serverless.ts
```

`make validate service=services/api-meet stage=dev` también requiere AWS/SLS autenticados y los parámetros SSM existentes, pues resuelve las variables de Serverless. Nunca usar producción para validar.

## Endpoints

| Método | Ruta | Operación |
| --- | --- | --- |
| GET | `/v1/reuniones?year=2026&month=10` | Listado mensual |
| POST | `/v1/reuniones` | Crear evento y espacio Meet |
| PUT | `/v1/reuniones/{eventId}` | Actualizar evento |
| DELETE | `/v1/reuniones/{eventId}` | Eliminar evento |

El POST acepta `idempotencyKey` (UUID generado por el cliente). Una repetición con la misma clave reutiliza el ID determinista de Calendar y devuelve el evento existente. Se crea un evento provisional sin avisar a los invitados antes de llamar a Meet; al agregar el enlace se notifica. Si Meet falla, una petición con clave puede reanudar la creación desde el evento provisional. Sin clave, se intenta eliminar el provisional.

**Limitación conocida:** la creación de Meet y la actualización final del evento son operaciones de dos APIs distintas, sin transacción distribuida. Si Meet crea un espacio pero la actualización de Calendar falla, podría quedar un espacio sin referenciar. Las creaciones estrictamente concurrentes con la misma clave tampoco están garantizadas como exactly-once sin una exclusión mutua distribuida.

## Validación de integración

GitHub Actions `Meet Quality` ejecuta pruebas unitarias, pruebas con HTTP simulado para Calendar/Meet, generación de cuatro artefactos ARM64, comprobación de `go.mod/go.sum` y TypeScript.

Para aprobar la integración real, en un calendario exclusivo de pruebas:

1. Comprobar scopes, propiedad/acceso del calendario y conexión del host.
2. Crear una reunión, revisar recepción de invitaciones y el enlace Meet.
3. Reintentar el mismo `idempotencyKey` y comprobar que no se duplica el evento.
4. Editar invitados, descripción y horario; verificar conservación del enlace Meet.
5. Eliminar el evento y comprobar que desaparece del calendario.
6. Verificar permisos de los perfiles y la aparición en el megamenú.
7. Restaurar el identificador del calendario DEV previsto, sin modificar registros reales.

No declarar producción lista hasta disponer de evidencia de todas estas pruebas.
