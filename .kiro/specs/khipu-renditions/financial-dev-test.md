# Prueba financiera controlada DEV

## Alcance

Autorizada por el usuario el 19 de octubre de 2026 al continuar el plan tras la observación. Usa exclusivamente los tres pagos simulados del reporte fijado en `dev-exception.md`. No acredita fondos reales, autenticidad del certificado ni fecha bancaria.

Implementación: `functions/renditionobserve/financial_dev_test.go`, protegida por build tag `devfinancial` y argumento explícito con el hash aprobado. No se ejecuta con `go test ./...`, no se incorpora a binarios Lambda y no modifica el receptor, la política OBSERVE ni el verificador estricto.

Comprueba perfil/cuenta DEV, configuración SecureString y caducidad, firma matemática con pin, importe total 104.245, tres correspondencias y comisiones cero antes de escribir. Reutiliza `Service.ReconcileSettlement`, con control de versión, referencia única, idempotencia y condición transaccional de ausencia de resolución del pago.

La fecha efectiva es el día de ejecución en Chile, explícitamente fecha de prueba. Actor `operator:dev-financial-test`; razón identifica simulación, certificado no confiable y hash. No se presenta como rendición autenticada del proveedor.

## Evidencia y recuperación

Antes de cualquier escritura financiera se guardan, con creación condicional, el estado previo y el manifiesto de comandos en el bucket privado de captura DEV:

`unverified/financial-test/<hash>/before.json`

`unverified/financial-test/<hash>/commands.json`

Contienen información privada; no imprimir ni incorporar al repositorio. Heredan lifecycle de siete días: no son respaldo permanente.

La prueba ejecuta dos veces los mismos tres comandos; exige el mismo evento y una sola subida de versión por liquidación. Después lee consistentemente liquidaciones, cuentas, eventos de pago, comandos, eventos de tesorería y ambas proyecciones de caja. Exige que las cuentas de pasajeros y eventos originales permanezcan iguales.

Si falla a mitad de ejecución, detener: no borrar evidencia ni volver a generar comandos para el mismo reporte. Inspeccionar los comandos persistidos y los tres registros exactos para determinar cuáles se aplicaron. No sobrescribir saldos ni hacer restauración manual del respaldo. Una nueva invocación se detiene ante liquidaciones ya aplicadas o respaldo existente; no constituye un mecanismo automático de recuperación.

Al completar se guarda `result.json` sin datos personales. El resultado solo valida la operación controlada y su replay, no el procesamiento automático de futuras notificaciones ni el despliegue financiero completo.

## Validación local

- `go test ./...` y `go vet ./...`: aprobados.
- Race de rendiciones/liquidaciones/reversas y de `renditionobserve`: aprobado.
- Vet y lint del paquete con tag `devfinancial`: aprobados.
- Sin argumento de confirmación, la prueba se omite sin escritura.
- Race global de collection: detecta una carrera preexistente en el contador del doble `checkoutGatewayFake.DevelopmentBank`, `provider_checkout_test.go:24`. No se modificó ese código ni se declara race global limpio.

## Resultado en AWS — 2026-10-19

Ejecución explícita completada: PASS (13,38 segundos). Tres liquidaciones simuladas conciliadas por CLP 104.245, comisiones cero. Los tres replays devolvieron exactamente los mismos eventos y liquidaciones, sin otro incremento de versión. Lecturas finales consistentes verificaron comandos, tesorería y proyecciones de caja; cuentas de pasajeros y eventos de cobro originales intactos. Se guardó `result.json` privado.

Los movimientos DEV permanecen registrados con su auditoría de prueba; no se eliminaron ni se revirtieron. La observación anterior que mostraba `settledGross:0` es histórica: ahora las tres liquidaciones están conciliadas. Producción no fue modificada. El receptor continúa siendo solo de captura; no se activó conciliación automática para reportes futuros.
