# Rendiciones Khipu

## Objetivo

Automatizar la recepción, cruce y aplicación de rendiciones agrupadas de Khipu sin duplicar caja. Activación inicial en observación: la fecha bancaria y el tratamiento de comisiones requieren contrastar el contrato real de la cuenta con una rendición y su cartola.

## Repos involucrados

| Repositorio | Responsabilidad |
| --- | --- |
| ind-hub-api | Verificación, inbox durable, proceso recuperable, consulta administrativa, infraestructura del servicio |
| ind-hub-orc | Contrato y procedimiento operativo de activación |
| ind-hub-app | Consulta de rendiciones, progreso y excepciones sin acciones financieras implícitas |

## Requisitos

- Validar gzip + JWS RS512, cadena X.509 y SAN khipu.com, o certificado explícitamente confiable; nunca confiar en certificados enviados sin validar su cadena.
- Vincular destinatario, banco y cuenta al ambiente; no registrar cuerpos ni datos personales en logs.
- Guardar evidencia antes de responder éxito; detectar rendición repetida o alterada.
- Soportar cientos de pagos en varias giras, con progreso recuperable y escrituras financieras atómicas por pago.
- Conservar referencia de rendición como padre de asignaciones; distinguirla de una referencia verificada en cartola.
- No estimar comisiones, inventar fecha bancaria ni duplicar conciliaciones manuales.
- Consultas con Query/GetItem, sin Scan. Reintentos durables programados y reanudación administrativa.
- Observación por defecto. Aplicación habilitada solo con política validada y documentada de fecha y comisiones.
- Recuperación de avisos omitidos: reenvío del mensaje firmado al mismo receptor; no se inventa una API de recuperación de Khipu.

## Fuentes verificadas

- https://docs.khipu.com/payment-solutions/instant-payments/payment-advanced
- https://docs.khipu.com/payment-solutions/instant-payments/payment-reports
- https://s3.amazonaws.com/static.khipu.com/rendiciones/jsonDiario/ejemploJsonDiario.json
