# Estado de entrega

## Implementado localmente

- [x] Verificación gzip/JWS RS512, confianza X.509, destinatario y montos exactos.
- [x] Evidencia privada en S3 antes de confirmar recepción; deduplicación semántica.
- [x] Procesamiento acotado, progreso durable, reintentos y revisión de excepciones.
- [x] Aplicación atómica e idempotente con protección frente a reversas y conciliaciones previas.
- [x] Política OBSERVE predeterminada; APPLY exige validación explícita de fecha y comisiones.
- [x] Comisiones descontadas y diferidas distinguibles; rechazo de políticas ambiguas.
- [x] Endpoints administrativos, consulta Angular y enlaces desde tesorería.
- [x] Funciones, programación y permisos del servicio definidos en código.
- [x] Procedimiento de activación, suspensión y recuperación documentado.

## Verificado localmente

- [x] `go test ./...` y `go vet ./...` en api-payment.
- [x] Detector de carreras en pruebas de rendiciones, conciliación y reversas.
- [x] Caso de 440 pasajeros distribuidos en 10 giras, sin doble aplicación.
- [x] Pruebas de firma, duplicados, recuperación, política, comisiones y excepciones.
- [x] Tres archivos / seis pruebas de UI y build Angular de producción.
- [x] Tipado de configuración Serverless y comprobación de funciones generadas.
- [ ] Lint global limpio: permanecen seis diagnósticos en código preexistente (public_lookup.go, startup.go, access_admin.go, persistence_test.go, public_receipt_verification.go y roster_closure_test.go). No se amplió esta entrega para corregirlos.
- [ ] QA visual en navegador: claro/oscuro y 320/390/tablet/desktop.

## Pendiente antes de activación

- [ ] Confirmar que la cuenta Khipu developer permite notificaciones de rendiciones, o conseguir un aviso oficial firmado de prueba.
- [ ] Aprovisionar configuración SecureString DEV con destinatario y confianza correctos, sin secretos en el repositorio.
- [ ] Desplegar en DEV y completar prueba de extremo a extremo en OBSERVE.
- [ ] Contrastar una rendición real con cartola y contrato de comisiones; validar significado de endDate antes de APPLY.
- [ ] Definir alertas operativas y responsable de reportes faltantes; no hay API histórica de recuperación confirmada.
- [ ] Autorizar y realizar despliegue productivo, primero en OBSERVE.

No se realizaron commits, despliegues productivos ni cambios de caja productiva. Las pruebas locales usan dobles de almacenamiento; no sustituyen la validación integrada de AWS y Khipu.

## Diagnóstico previo DRN-2.0

- [x] Receptor temporal DEV independiente, sin permisos financieros, con captura privada limitada y vencimiento.
- [x] Pruebas locales, race, vet y lint del nuevo paquete sin errores; compilación Linux ARM64.
- [x] Stack temporal creado en DEV y prueba HTTP inicial 200 con evidencia sintética en S3.
- [x] Verificar cifrado, bloqueo público, lifecycle y permisos del rol desplegado.
- [x] Actualización final UPDATE_COMPLETE, hash del ZIP cotejado con Lambda y smoke repetido sin duplicación.
- [ ] Recibir una notificación auténtica de Khipu DRN-2.0 y verificar firma/formato/destinatario.

Ver `capture-dev.md`: la captura no es la conciliación financiera y ambos stacks no pueden ser propietarios simultáneos de la ruta.

## Excepción diagnóstica DEV — 2026-10-19

- [x] Capturar reporte developer de tres pagos; firma matemática válida, certificado no confiable para el verificador estricto.
- [x] Implementar procesador separado OBSERVE, limitado a cuenta DEV, certificado y reporte exactos, con caducidad.
- [x] Aprovisionar política SecureString temporal DEV y observar tres coincidencias por CLP 104.245, sin escrituras de caja.
- [x] Repetir contra AWS: duplicado reconocido, un único resumen S3 y liquidaciones todavía en cero.
- [ ] Autorizar y ejecutar prueba financiera controlada DEV; no incluida en esta observación.

Ver `dev-exception.md`. Esta excepción no completa la validación de autenticidad ni el despliegue de extremo a extremo del flujo financiero.
