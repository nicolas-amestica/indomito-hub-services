# Validación local — 18 de noviembre de 2026

## Evidencia ejecutada

- Frontend: `npm run build`, producción, compilación correcta; `npm run check:buttons`, correcto.
- Frontend: `ng test --watch=false` limitado a analytics y motor de cálculo existente: **114 pruebas, 14 archivos**, correctas. Cubren liberados, bases especiales, días/noches distintos, divisas y redondeo, precio fijo, objetivo, margen/recargo, retención incluida, cero pagantes, falta de presupuesto, escalones/capacidad, importación histórica, relación parcial con proveedores, proyección de déficit, paginación, respuesta obsoleta y reintento idempotente.
- Backend: `go test -race ./services/api-analytics/...`, correcto; `go vet`, correcto; `golangci-lint`, cero incidencias. Cubren validación, aprobación explícita, independencia de escenarios, conservación de línea base, versión obsoleta, replay exacto, escritor concurrente, tablas de destino, cantidades contractuales, revisión paginada, identidad ausente y conciliación del historial contra cuotas pagadas.
- Backend: build de las tres Lambdas ARM64 y ZIP; validador de servicio: **15 checks correctos**.
- OpenAPI: JSON analizado, tres operaciones, referencias internas comprobadas. El CLI Postman instalado no soporta `status`; no se inicializó ni publicó un workspace.

## Navegador con API interceptada y datos ficticios

Pruebas temporales ejecutadas con Playwright contra la SPA local, nunca contra pagos reales. 320, 390, 768 y 1440 px × temas claro/oscuro × cinco secciones: **40 comprobaciones**, sin desbordamiento ni errores de JavaScript. Se cargaron realmente los 17 canvas, más la tabla del mapa de calor. Axe WCAG A/AA no reportó infracciones en la revisión de las cinco secciones. Se probaron los comandos SAVE, APPROVE y SCENARIO y una descarga de Excel. El libro generado se abrió como OOXML: hojas Alcance/Viajes/Escenario seleccionado; fórmulas B7/B8/B10/B11 presentes y resultados almacenados coherentes con el escenario de prueba.

La prueba detectó y permitió corregir una colisión del output `change` con eventos nativos de los campos; el editor utiliza `budgetChange`. También se corrigieron contraste local de severidades y límites de ancho de paneles de gráficos a 320 px.

## Alcance y límites

- No se invocaron bancos, proveedores de pago ni SII. No se desplegó ni ejecutó el bootstrap IAM en DEV o PRD.
- La navegación de Analítica aparecerá al registrar los módulos en la configuración autoritativa y renovar permisos; no hay menú paralelo.
- Requiere el perfil exacto ADM existente. El script se detiene si no existe; no usa ADMIN como sustituto.
- Snapshots antiguos sin divisas/impuestos completos no se recotizan con valores actuales. Se ofrece presupuesto manual y procedencia explícita.
- La curva de cuotas compara el calendario vigente con cobros aplicados verificables; no representa el calendario original anterior a anexos. Sin cobertura suficiente muestra explicación.
- Caja y financiamiento usan caja registrada en Hub, no un saldo bancario anterior no respaldado. El faltante actual se habilita con consulta hasta hoy; la proyección se marca parcial mientras falten fechas o confirmación de cobertura.
- Compromisos operativos y costos netos se comparan, pero no se suman automáticamente porque no existe una base tributaria homogénea certificada. Se informan compromisos sin asociar.
- Recursos nuevos: tres Lambdas, roles y logs. Se reutilizan tablas e índice documental; sin colas, índices nuevos, librerías adicionales ni plataforma analítica pesada.

## Archivos por repositorio

### Backend

- `common/api-services.ts`
- `go.work`
- `scripts/configure-analytics-iam-dev.py`
- `services/api-analytics/README.md`
- `services/api-analytics/cmd/fn-guardar-balance-v1/main.go`
- `services/api-analytics/cmd/fn-listar-balance-v1/main.go`
- `services/api-analytics/cmd/fn-obtener-balance-v1/main.go`
- `services/api-analytics/cmd/local-api/main.go`
- `services/api-analytics/docs/examples/approve.request.json`
- `services/api-analytics/docs/examples/conflict.response-409.json`
- `services/api-analytics/docs/examples/promote.request.json`
- `services/api-analytics/docs/examples/save-budget.request.json`
- `services/api-analytics/domain/analytics/budget.go`
- `services/api-analytics/domain/analytics/budget_test.go`
- `services/api-analytics/functions/analytics_admin.go`
- `services/api-analytics/functions/analytics_read.go`
- `services/api-analytics/functions/analytics_store.go`
- `services/api-analytics/functions/analytics_test.go`
- `services/api-analytics/functions/app.go`
- `services/api-analytics/functions/guardar-balance-v1/endpoint.go`
- `services/api-analytics/functions/listar-balance-v1/endpoint.go`
- `services/api-analytics/functions/obtener-balance-v1/endpoint.go`
- `services/api-analytics/functions/routes.go`
- `services/api-analytics/go.mod`
- `services/api-analytics/go.sum`
- `services/api-analytics/openapi.fragment.json`
- `services/api-analytics/serverless.ts`
- `services/api-analytics/service.config.json`

Spec única: `requirements.md`, `design.md`, `tasks.md` y este informe en `.kiro/specs/analytics-balance/`.

### Frontend Hub

- `src/app/app.routes.ts`
- `src/app/indomito-hub/analytics/analytics.routes.ts`
- `src/app/indomito-hub/analytics/components/balance-chart.component.ts`
- `src/app/indomito-hub/analytics/components/budget-editor.component.html`
- `src/app/indomito-hub/analytics/components/budget-editor.component.ts`
- `src/app/indomito-hub/analytics/constants/balance-columns.ts`
- `src/app/indomito-hub/analytics/constants/balance-design-tokens.ts`
- `src/app/indomito-hub/analytics/constants/balance-options.ts`
- `src/app/indomito-hub/analytics/fn/__fixtures__/balance.fixture.ts`
- `src/app/indomito-hub/analytics/fn/balance-id.ts`
- `src/app/indomito-hub/analytics/fn/balance-table.ts`
- `src/app/indomito-hub/analytics/fn/balance.spec.ts`
- `src/app/indomito-hub/analytics/fn/build-balance-charts.spec.ts`
- `src/app/indomito-hub/analytics/fn/build-balance-charts.ts`
- `src/app/indomito-hub/analytics/fn/calculate-balance.ts`
- `src/app/indomito-hub/analytics/fn/create-budget.ts`
- `src/app/indomito-hub/analytics/fn/export-balance.ts`
- `src/app/indomito-hub/analytics/fn/financial-metrics.ts`
- `src/app/indomito-hub/analytics/fn/operating-balance.ts`
- `src/app/indomito-hub/analytics/interfaces/balance.interface.ts`
- `src/app/indomito-hub/analytics/pages/balance-detail.page.html`
- `src/app/indomito-hub/analytics/pages/balance-detail.page.ts`
- `src/app/indomito-hub/analytics/services/balance.service.ts`
- `src/app/indomito-hub/analytics/stores/balance.store.spec.ts`
- `src/app/indomito-hub/analytics/stores/balance.store.ts`
- `src/app/indomito-hub/analytics/types/balance-chart.ts`

### Orquestador

- `specs/features/analytics-balance/api-contract.md`

Authorizer, infraestructura, Indómito Pay y proyectos legacy: sin cambios. No se agregaron dependencias.

## Actualización autorizada posterior: IAM PRD

El acceso IAM de aplicación se creó y verificó en PRD por solicitud explícita del usuario. Se preservaron los 16 registros anteriores de ADM y la reejecución produjo cero cambios. No hubo despliegues. Evidencia y ajustes concurrentes conservados: `iam-prd-activation.md`.
