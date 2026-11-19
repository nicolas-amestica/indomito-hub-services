# Tareas — implementación local

- [x] Revisar steering, contratos y motor; documentar discrepancias de identificadores, autenticación, specs antiguas y pipeline OpenAPI.
- [x] Especificación única con aprobación explícita, ambos indicadores de financiamiento y lenguaje sencillo.
- [x] Servicio independiente api-analytics, por decisión del usuario; sin cambios a api-payment ni authorizer.
- [x] Contrato API, presets de entrada, persistencia versionada, auditoría, idempotencia y concurrencia.
- [x] Catálogo documental paginado; contratos sin deuda incluidos; fechas operativas vigentes separadas.
- [x] Consolidación operativa en servidor, calendarios y movimientos agregados; historial de cuotas con cobertura verificable.
- [x] Bootstrap IAM idempotente para el perfil exacto ADM, preparado sin ejecución.
- [x] Presupuesto manual e importación histórica sin duplicar; pagantes/liberados explícitos.
- [x] Motor de simulación reutilizado: precio fijo, objetivo, monedas, cantidades especiales, mínimos, escalones y capacidad.
- [x] Cinco secciones, 17 gráficos y mapa de calor accesible, filtros, comparación de alternativas y exportación Excel.
- [x] Build de producción, check:buttons y pruebas frontend; pruebas Go con -race, vet, lint, artefactos ARM64 y 15 validaciones estructurales.
- [x] Prueba en navegador con datos ficticios: 40 combinaciones de sección, ancho y tema; flujo guardar/aprobar/escenario/Excel; revisión axe.
- [ ] Activación DEV: desplegar servicios/frontend y ejecutar bootstrap IAM tras autorización específica. Fuera de esta ejecución local.
- [ ] Validación integrada con datos DEV y aceptación del usuario. No se presenta la evidencia con datos ficticios como resultado real.

No se marca la funcionalidad como desplegada ni la spec como mergeada. No hubo commits, push, publicación ni cambios a producción.
