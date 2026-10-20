# Automatización permanente DEV

## Decisión del usuario — 2026-10-19

Se autoriza explícitamente no exigir certificado confiable en DEV, sin vencimiento, para reportes futuros. Esta decisión sustituye el pin de un único reporte del diagnóstico anterior, pero no modifica producción. No constituye autenticación de Khipu: cualquier emisor capaz de firmar un reporte con su propio certificado puede presentarlo. DEV debe contener exclusivamente datos y movimientos de prueba.

Se mantiene gzip/JWS RS512, RSA >= 2048, firma matemática, límites de tamaño/expansión, fechas, destinatario configurado, importes enteros, cruce con pagos confirmados, control de versión y no duplicación. Sin certificado adjunto ni clave configurada no es posible verificar una firma: se rechaza. La excepción omite CA, SAN y vigencia, no integridad.

El método estricto `RenditionVerifier.Verify` continúa rechazando certificados no confiables. El nuevo `DevelopmentRenditionVerifier` exige ambiente dev y cuenta 382670112717. El entrypoint DEV comprueba además ARN, región y nombre exacto de función antes de leer configuración. Los entrypoints normales rechazan políticas developmentSimulation.

## Recursos

Se actualiza el stack existente `ind-khipu-rendition-capture-dev`, conservando su ruta e integración. La plantilla `rendition-capture-dev.yml` pasa de captura temporal a automatización DEV. No desplegar simultáneamente la ruta final de api-payment sobre ese recurso.

- Receptor: `ind-khipu-rendition-capture-dev`, solo evidencia S3 e inbox DynamoDB; sin permisos de caja.
- Trabajador: `ind-khipu-rendition-worker-dev`, programación cada minuto, capacidad compartida sin reserva, máximo diez lotes/cincuenta ítems por lote en cada ejecución. AWS rechazó reservar concurrencia por el mínimo sin reservar de la cuenta; se mantiene idempotencia y control transaccional de versión ante solapamientos.
- Configuración SecureString: `/indomito/dev/payments/khipu/renditions/automation-config`, sin until ni pin. Destinatario obtenido de la muestra DEV aprobada, no verificado contra cartola.
- Política APPLY simulada: feeScheme developer exacto, comisiones inmediatas/diferidas cero; otros casos requieren revisión. Fecha endDate utilizada solamente como fecha simulada del reporte.
- Estado etiquetado `DEV_UNAUTHENTICATED_SIMULATION`; actor de caja `system:dev-rendition-simulation` y razón explícita de prueba.
- Nuevos originales/resúmenes S3 bajo `renditions/dev/`, privados y con retención 30 días. Evidencia anterior `unverified/` conserva sus siete días.
- Reintentos durables: hasta doce fallos, separados por cinco minutos, luego REVIEW_REQUIRED. Fallos se reflejan en Lambda Errors; no se depende de reintentos automáticos de Lambda.
- Cinco alarmas: errores del receptor, errores del trabajador, falta de invocaciones, reportes rechazados y casos en revisión. Tema SNS `ind-khipu-rendition-dev-alerts`, suscripción email solicitada por el usuario mediante parámetro AlertEmail. Requiere confirmar el enlace de AWS; configurar destinatario no demuestra entrega. Recursos serverless de bajo volumen; las alarmas/métricas pueden generar cargos según el uso y beneficios vigentes de la cuenta.

No se instalan nuevas interfaces administrativas en este stack. Las liquidaciones se reflejan en los registros de tesorería existentes; el despliegue de la pantalla/inbox de rendiciones pertenece al servicio y frontend completos.

## Operación

Conservar la URL `/pagos/khipu/rendiciones` configurada en la cuenta developer. No modificar `/pagos/khipu/notificaciones` ni su versión 3.0. No cambia la configuración de Khipu PRD.

Para suspender aplicación: cambiar únicamente policy.mode a OBSERVE mediante SSM seguro; la política vigente bloquea la continuación de lotes APPLY. No borrar asientos. No trasladar este SecureString a PRD. Para retirar la excepción, sustituir el entrypoint DEV por el flujo estricto mediante un cambio de infraestructura revisado, sin dos propietarios de ruta.

## Antes de producción

1. Configuración independiente de destinatario y certificados, sin developmentSimulation.
2. Notificación productiva auténtica, firma/cadena/SAN válidos y comparación con cartola, fecha y contrato de comisiones.
3. Despliegue productivo explícitamente autorizado, inicialmente OBSERVE; confirmar autorización de rutas administrativas y UI.
4. Alertas con responsable y suscripción confirmada; monitoreo de pagos en tránsito no rendidos. Una alarma de worker activo no detecta por sí sola que Khipu omitió un reporte.
5. APPLY solo con reglas financieras reales validadas. El éxito DEV no sustituye estas condiciones.

Referencia: [verificación oficial de rendiciones](https://docs.khipu.com/payment-solutions/instant-payments/payment-advanced). La excepción DEV es una decisión de pruebas del usuario, no una recomendación de Khipu.
