# Prueba DEV y activación de rendiciones

## Estado inicial

El usuario confirma que hoy se realizó el primer pago productivo; todavía no se ha contrastado su rendición con cartola. También dispone de una cuenta Khipu developer. No confundir confirmación de ese pago con recepción de rendición.

Inspección de AWS DEV, sin leer valores: existen api-key, receiver-id y webhook-secret como SecureString. No existe `/indomito/dev/payments/khipu/renditions/config`. Ningún secreto debe pegarse en chats, logs o archivos versionados.

## Configuración

Aprovisionar mediante el mecanismo seguro de SSM un SecureString independiente por ambiente:

`/indomito/{dev|prd}/payments/khipu/renditions/config`

Esquema inicial (completar en SSM; los valores vacíos se rechazan):

```json
{
  "recipientUid": "",
  "bank": "",
  "account": "",
  "certificatePem": "",
  "policy": { "mode": "OBSERVE" }
}
```

Los datos deben coincidir exactamente con el destinatario del reporte de esa cuenta. Para mensajes con x5c se valida cadena contra CA del sistema y SAN khipu.com. Si el aviso no incluye x5c, configurar el certificado oficial correspondiente al ambiente en certificatePem. No usar un certificado aportado por un aviso sin verificarlo ni mezclar certificados DEV y PRD. No introducir llaves privadas.

## Despliegue y prueba

**Actualización del panel developer:** las capturas del usuario muestran DRN-1.0 y DRN-2.0 para rendiciones, no 3.0. La equivalencia con el formato de la documentación no está confirmada. Antes del despliegue financiero seguir [la captura diagnóstica DEV](capture-dev.md). No desplegar ambos stacks con la misma ruta.

1. Completar configuración de DEV, preparar artefactos y desplegar api-payment en DEV usando el procedimiento habitual. La ruta está en el Gateway compartido: comprobar su URL real; no asumir DNS listo.
2. En la cuenta **developer** de Khipu configurar la URL HTTPS real más `/pagos/khipu/rendiciones` solo después de comprobar el receptor. Probar DRN-2.0 únicamente con captura diagnóstica hasta contrastar su formato; la referencia a 3.0 en la documentación pública no coincide con el selector del panel. Esta URL es distinta del webhook de pagos y no lo reemplaza.
3. Confirmar si la cuenta developer emite rendiciones o permite solicitar un aviso de prueba. DemoBank documenta pagos exitosos/dudosos/fallidos, pero ello no demuestra disponibilidad de rendiciones. Si no hay avisos, solicitar a Khipu un sobre firmado de prueba; no habilitar bypass de firma.
4. Recibir un reporte firmado con pagos reconocibles de DEV. Comprobar HTTP 200, evidencia privada, lote OBSERVED, pagos correctos y caja sin cambios. Ver `/cobranza/tesoreria/rendiciones` (permiso PAYMENT_OPERATIONS y GET /pagos/tesoreria).
5. Reenviar el mismo aviso válido y comprobar un solo lote. Simular interrupción y comprobar recuperación sin duplicación. Contrastar pagos de varias giras y casos manualmente conciliados.
6. En PRD empezar también en OBSERVE, luego de desplegar de forma explícitamente autorizada. Comparar el primer reporte con cartola: destinatario, suma txAmount, comisión descontada/diferida, fecha efectiva y código bancario. Conservar evidencia fuera del repositorio.

## Habilitar aplicación

Solo después de validar los campos reales: cambiar policy a APPLY con `revision`, `validatedBy`, `evidence`, `effectiveDateSource: "REPORT_END"` y listas exactas de `deductedFeeSchemes` / `deferredFeeSchemes`. Si endDate no es una fecha apta para el registro de caja, **no activar**: adaptar el contrato al dato real o incorporar confirmación bancaria por lote.

La política es una instantánea por lote. Los lotes ya observados se reanudan usando POST `/pagos/tesoreria/rendiciones/{id}/reintentos` con permiso administrativo de escritura; pueden modificar caja bajo la política actual. El historial anterior se conserva. El reintento no inicia cobros ni transferencias.

Para detener nuevas aplicaciones, establecer OBSERVE: el trabajador impide continuar lotes APPLY bajo un modo/revisión revocados. Los asientos ya aplicados no se borran ni revierten automáticamente. Hasta cinco minutos pueden transcurrir entre reintentos; una ejecución ya iniciada puede completar su bloque.

## Control operativo

- Revisar REVIEW_REQUIRED y los pagos Khipu en tránsito que no aparezcan en rendiciones.
- Si falta un aviso, pedir reenvío firmado a Khipu o usar su procedimiento oficial confirmado. No inventar IDs ni comisiones.
- Alarmas recomendadas de Lambda Errors/Throttles y seguimiento de lotes PENDING envejecidos; definir destinatario de alertas antes de operación desatendida. Esta entrega no crea suscripciones ni envía mensajes.
- No compartir el original S3: incluye datos personales. Los endpoints administrativos solo publican resumen financiero.

Fuentes: [rendiciones y verificación](https://docs.khipu.com/payment-solutions/instant-payments/payment-advanced), [reportes](https://docs.khipu.com/payment-solutions/instant-payments/payment-reports), [pruebas DemoBank](https://docs.khipu.com/payment-solutions/instant-payments/khipu-client-test-cases).
