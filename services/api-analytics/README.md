# api-analytics — Detalle balance

Servicio HTTP privado de planificación operativa. Comparte Gateway y authorizer existentes. No modifica api-payment, el authorizer ni infraestructura compartida.

## Frontera de datos

- **Programas**: presupuestos y escenarios, partición `TRIP#<ULID>`, claves `ANALYTICS#CURRENT`, `ANALYTICS#REV#<version>` y `ANALYTICS#COMMAND#<ULID>`.
- **Pagos, solo lectura**: cuentas por `ACCOUNT#id/META`, nómina `TRIP#id/MEMBER#`, compromisos `SUPPLIER#`, liquidaciones `SETTLEMENT#`, eventos bancarios `CASH#` y eventos de cuenta `EVENT#`.
- **Contratos**: `CONTRACT#id/METADATA` y GSI documental existente. El ID contractual identifica el viaje en el dominio actual.
- El import del dominio `api-payment/domain/collection` reutiliza `Account.Validate`, `Account.Position` y `ProjectCash`. No importa sus handlers, proveedores, secretos ni operaciones de escritura.
- El rol de guardado permite PutItem sobre Programas y no tiene escritura en Pagos. Cada comando conserva estado, revisión y recibo en una sola transacción con condición de versión.
- El motor comercial sigue en TypeScript. El servidor valida entradas y relaciones; no guarda totales calculados como verdades contables ni crea compromisos desde escenarios.

## Contrato y autorización

`openapi.fragment.json` es el contrato mantenido, según common-backend-rules del orquestador. La spec única está en `.kiro/specs/analytics-balance/` del backend.

- GET `/analitica/giras?year=2026&from=2026-01-01&to=2026-11-18`: catálogo paginado por período documental, incluidos viajes sin deuda. Procesar `nextCursor` hasta vacío.
- GET `/analitica/giras/{id-viaje}`: presupuesto y datos operativos agregados. `version` permite recuperar una revisión histórica; los datos operativos siguen siendo posición actual. `revisions` contiene auditoría, no copias ilimitadas de presupuestos.
- PUT `/analitica/giras/{id-viaje}/presupuesto`: `commandId`, `version`, `action`, `reason`, `scenarioId`, `budget`.
- Acciones: SAVE (borrador/estimación), SCENARIO (alternativa), APPROVE (línea base explícita), PROMOTE (adopción explícita del escenario).
- Idempotencia: reintentar exactamente el mismo comando. Otra intención con el mismo ID devuelve conflicto. No reemplazar la versión para vencer un conflicto sin revisar la actualización ajena.
- Gateway aplica las concesiones IAM por ruta/método, como los otros módulos. Las funciones verifican identidad del contexto y las reglas de dominio; no leen identidad del JSON ni revalidan JWT.

## Fórmulas y cobertura

La documentación de fórmulas y bases vive en el diseño de la spec. Caja registrada no equivale a saldo bancario conciliado de una cuenta, ni a utilidad. La posición es actual aunque el rango de movimientos sea anterior. La curva de cuotas compara cobros aplicados verificables con el calendario vigente; no reconstruye el plan original anterior a anexos. Si el historial no reconcilia con cuotas pagadas o hubo asignaciones no reconstruibles, se marca incompleto.

Los compromisos están expresados en CLP operativos sin una base tributaria neta certificada. Por eso se comparan y enlazan, pero no se suman automáticamente al costo neto presupuestado. Las asignaciones parciales identifican costos faltantes; el operador debe ingresar y validar la partida correspondiente. Las comisiones reales informadas en liquidaciones no se vuelven a descontar de ingresos bancarios ni se agregan automáticamente al presupuesto.

Los escenarios pueden tener hasta 120 partidas y seis alternativas. Cada payload de comando se limita a 190 kB y el estado a 290 kB para mantener los límites transaccionales de DynamoDB. Se conserva todo el historial; no se generan históricos con tasas actuales.

## Validación local

```sh
go test -race ./services/api-analytics/...
go vet ./services/api-analytics/...
golangci-lint run ./services/api-analytics/...
npx tsx scripts/build-service.ts --service services/api-analytics
npx tsx scripts/validate-service.ts --service services/api-analytics --stage dev --region us-east-1
```

Los binarios Lambda son ARM64 y no despliegan por compilar. El servidor de desarrollo usa `127.0.0.1:8098` y el adaptador local compartido del proyecto, que asigna identidad de desarrollo: no representa una verificación del authorizer real.

## IAM por ambiente

`scripts/configure-analytics-iam-dev.py` exige cuenta esperada y verifica que exista el perfil exacto `ADM`. Solo configura LV1 `ANALYTICS` y LV2 `ANALYTICS_BALANCE_DETAIL`; la lectura es `r`, la gestión es `u` (todos los comandos son PUT). No reemplaza permisos ajenos ni concede a ADMIN u otros perfiles. Sin `--apply` solo muestra los cambios; aun así consulta identidad, stack y registros en DEV. Este trabajo no ejecuta el script.

## Recursos adicionales

Tres Lambdas HTTP, roles y grupos de logs del builder existente. Reutiliza ambas tablas e índice documental; sin tablas, índices, colas, jobs ni librerías nuevas. El costo incremental depende de consultas DynamoDB, almacenamiento de revisiones, invocaciones Lambda y logs. No se estima un costo monetario sin volumen observado.

Las respuestas agrupan vencimientos y cobros por fecha y caja por día; no publican eventos ni identidades de cuentas. La venta contractual operativa vigente corresponde a cobrado aplicado + deuda vigente − devoluciones por bajas aprobadas, con las condiciones actuales de cobranza; no es ingreso contable.

Actualización posterior autorizada: el acceso IAM de PRD ya fue creado y verificado; ver `.kiro/specs/analytics-balance/iam-prd-activation.md`. El bootstrap admite `--stage dev|prd`, con DEV por defecto. Esta activación de permisos no implica despliegue del módulo.
