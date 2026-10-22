<!-- AUTO-GENERATED — DO NOT EDIT MANUALLY -->
<!-- Managed-By: indomito-context-compiler -->
<!-- Artifact-Format: 1 -->
<!-- Engine-Version: 1.0.0 -->
<!-- Source: ai/source/skills/jira-task-close.md -->
# Registrar y cerrar tarea en Jira

Crea una tarea en Jira, agrega un comentario técnico completo y la transiciona a un estado cuya categoría sea `done`. El registro debe permitir que otra persona entienda qué ocurrió, cómo se verificó y cómo desplegarlo sin consultar la conversación original.

## Configuración

Usar las credenciales locales del repositorio orquestador:

```text
tools/config/.env
```

Variables requeridas:

```text
JIRA_BASE_URL
JIRA_USER_EMAIL
JIRA_API_TOKEN
```

Valores predeterminados:

- Proyecto: `IH` — Indómito Hub.
- Tipo: `Tarea`.
- API: Jira Cloud REST API v3.
- Descripción y comentarios: Atlassian Document Format (ADF).

## Reglas de seguridad

- Ejecutar la integración desde `ind-hub-orc-aws-sls-pri-gh`.
- Nunca buscar ni leer `.env` mediante herramientas que impriman coincidencias completas.
- Nunca mostrar, registrar, copiar ni incluir `JIRA_API_TOKEN` en respuestas, comandos visibles, archivos temporales o artefactos.
- Cargar las variables dentro del proceso con `set -a; source tools/config/.env; set +a`.
- Autenticar con Basic Auth usando `JIRA_USER_EMAIL:JIRA_API_TOKEN` únicamente en la llamada HTTPS a Jira.
- No incluir secretos, cuerpos de webhooks, datos personales, payloads productivos ni credenciales AWS en Jira.
- No cerrar la tarea si falla el comentario o su verificación.
- No asumir que la transición se llama `Done`, `Cerrar` o `Finalizar`; seleccionar una cuya propiedad `to.statusCategory.key` sea `done`.

## Información requerida

Reunir desde la conversación y el estado real del workspace:

1. Título breve.
2. Fecha de inicio real en formato `YYYY-MM-DD`.
3. Fecha de término real en formato `YYYY-MM-DD`.
4. Contexto y objetivo.
5. Problema o causa raíz, cuando corresponda.
6. Acciones y decisiones técnicas.
7. Resultado final por ambiente.
8. Validaciones ejecutadas y sus resultados reales.
9. Repositorios involucrados y tipo de participación.
10. Commits y pull requests relacionados, si existen.
11. Comandos exactos para desplegar a dev y prd.
12. Pendientes, riesgos y límites de la validación.

Derivar las fechas desde evidencia verificable de la conversación, commits, despliegues o logs. Si no es posible determinar una fecha con certeza, preguntarla antes de crear la tarea. La fecha de término no puede ser anterior a la fecha de inicio.

Si falta un dato imprescindible que no puede derivarse del chat, Git o los archivos, preguntarlo antes de crear la tarea. No inventar resultados, comandos, commits ni estados.

## Flujo obligatorio

### 1. Verificar acceso y metadatos

Consultar:

```text
GET /rest/api/3/myself
GET /rest/api/3/project/search
GET /rest/api/3/issue/createmeta/IH/issuetypes
```

Confirmar que la cuenta está activa, que `IH` es accesible y obtener dinámicamente el id del tipo `Tarea`. Conservar el `accountId` retornado por `/myself` y usarlo para asignar la tarea; no intentar asignar mediante correo directamente, porque Jira Cloud exige `accountId`. La identidad autenticada debe corresponder a `JIRA_USER_EMAIL`; si Jira oculta el correo por privacidad, `/myself` sigue siendo la fuente de verdad de la cuenta asociada a esas credenciales.

Consultar además los campos disponibles para creación mediante `createmeta` con expansión de campos. Identificar dinámicamente el campo de fecha de inicio por nombre localizado (`Fecha de inicio` o `Start date`) y esquema. No asumir un id `customfield_*` fijo.

### 2. Crear la tarea

Usar:

```text
POST /rest/api/3/issue
```

Campos mínimos:

- `project.key`: `IH`.
- `issuetype.id`: id descubierto para `Tarea`.
- `summary`: título concreto en español.
- `description`: ADF con objetivo y alcance.
- `assignee.accountId`: `accountId` obtenido desde `/myself` usando las credenciales de `JIRA_USER_EMAIL`.
- Campo de fecha de inicio descubierto dinámicamente: fecha real de inicio en `YYYY-MM-DD`.
- `duedate`: fecha real de término en `YYYY-MM-DD`.
- `labels`: etiquetas técnicas relevantes, en minúsculas y sin datos sensibles.

Conservar `key` y URL. Ante error, mostrarlo redactado y detenerse.

### 3. Agregar comentario técnico

Usar:

```text
POST /rest/api/3/issue/{issueKey}/comment
```

El comentario ADF debe contener, cuando apliquen:

1. `Resumen del trabajo`
2. `Contexto y causa raíz`
3. `Acciones realizadas`
4. `Resultado final`
5. `Validaciones y evidencia`
6. `Repositorios involucrados`
7. `Comandos de despliegue`
8. `Commits y pull requests`
9. `Pendientes y consideraciones operativas`

### Repositorios

Por cada repositorio indicar:

```text
<repo>: <cambio>; <ambientes desplegados>; <commit o “sin cambios de código”>.
```

Distinguir entre repositorio modificado, desplegado, inspeccionado sin cambios y no involucrado.

### Comandos

Incluir bloques separados para dev y prd, indicando desde qué directorio se ejecutan. Los comandos deben ser completos y ejecutables.

Ejemplo:

```bash
# Desde la raíz del repositorio correspondiente
make deploy service=services/api-payment stage=dev region=us-east-1
make deploy service=services/api-payment stage=prd region=us-east-1
```

Agregar validaciones previas cuando existan, sin sustituir los comandos de despliegue.

### 4. Verificar el comentario

Consultar:

```text
GET /rest/api/3/issue/{issueKey}?fields=summary,project,issuetype,status,comment
```

Confirmar que título, proyecto y tipo son correctos; que la tarea está asignada al `accountId` obtenido desde `/myself`; que las fechas de inicio y término coinciden con las solicitadas; que el comentario existe; y que contiene repositorios y comandos para ambos ambientes. Si falla, no cerrar.

### 5. Cerrar la tarea

Consultar:

```text
GET /rest/api/3/issue/{issueKey}/transitions?expand=transitions.fields
```

Elegir una transición con:

```text
to.statusCategory.key == "done"
```

Si hay varias, preferir semánticamente `Completada`/`Done`, luego `Cerrada`/`Closed` y finalmente `Finalizada`/`Resolved`.

Ejecutar:

```text
POST /rest/api/3/issue/{issueKey}/transitions
```

con el id correspondiente. Si no hay transición directa a `done`, informar el bloqueo y dejar la tarea creada y comentada; no forzar estados intermedios.

### 6. Verificación final

Volver a consultar la tarea y comprobar:

- `status.statusCategory.key == "done"`.
- La asignación corresponde al `accountId` de `/myself`.
- Las fechas de inicio y término permanecen registradas.
- El comentario sigue presente.
- La URL es navegable.

La respuesta final debe entregar clave, enlace, título, estado final, confirmación del comentario, repositorios documentados, comandos dev/prd incluidos y pendientes reales.

## Construcción de ADF

Usar nodos válidos:

- `doc` raíz con `version: 1`.
- `heading` para secciones.
- `paragraph` para texto.
- `bulletList` y `listItem` para acciones y repositorios.
- `codeBlock` con lenguaje `bash` para comandos.
- `strong` solo para etiquetas breves.

No enviar Markdown plano esperando conversión automática.

## Criterios de cierre

La ejecución está completa solo si:

- La tarea se creó en `IH` como `Tarea` y quedó asignada al usuario autenticado mediante `JIRA_USER_EMAIL`.
- Las fechas reales de inicio y término quedaron registradas y verificadas.
- El comentario se creó y se leyó nuevamente desde Jira.
- Repositorios y comandos dev/prd están presentes.
- La tarea terminó en categoría `done`.
- La respuesta incluye el enlace verificable.

Si algún punto falla, indicar cuál quedó pendiente y no afirmar que la tarea fue cerrada.
