# Recuperación de conciliación Khipu

## Creación con respuesta perdida

`ClaimCheckoutDispatch` guarda en una sola transacción la autorización única de
envío y un trabajo en `RECONCILIATION#PENDING`, antes del POST a Khipu. Si esa
transacción no confirma éxito al llamador, no se ejecuta el POST. Si Khipu crea el
cobro pero se pierde su respuesta o falla el guardado de CHECKOUT, el trabajo ya
existe y conserva el bloqueo de la cuota.

El consumidor comprueba primero si existe CHECKOUT. Si falta, busca un resultado
financiero o resolución previamente verificados. Si los encuentra, cierra el
trabajo sin repetir movimientos. Si no existen, informa
`PROVIDER_REVIEW_REQUIRED` con detalle interno `CHECKOUT_RESPONSE_MISSING`,
reprograma el trabajo y publica la métrica `Indomito/Payments` /
`CheckoutRecoveryRequired` por ambiente. La alarma productiva reutiliza SNS y
SMTP de `payment-monitoring`; agrega una métrica personalizada y una alarma
estándar, sujetas a la cuota gratuita disponible y al costo de CloudWatch.

La API v3 publicada no documenta una consulta por `transaction_id` ni una
garantía de idempotencia del POST de creación. Por eso nunca se repite el POST
para intentar recuperar un `payment_id` desconocido:

- [Descriptor OpenAPI v3](https://docs.khipu.com/_bundle/apis/v3/instant-payments/openapi.json?download=)
- [Creación de cobros](https://docs.khipu.com/apis/v3/instant-payments/openapi/other/postpayment)
- [Notificaciones](https://docs.khipu.com/payment-solutions/instant-payments/payment-webhook)

El webhook autenticado aporta `payment_id` y `transaction_id`; después se
consulta Khipu para validar identidad e ingreso antes de contabilizar. Si no
llega una confirmación, el operador debe recuperar el ID real desde Khipu y
usarlo en Cobranza > intento > conciliación. El conciliador verifica que
corresponda a la misma transacción y cobrador. Una respuesta perdida, un timeout
o un checkout ausente nunca autorizan liberar la cuota. Una respuesta tardía
que sí alcance a guardarse será utilizada por el siguiente trabajo, sin
recrear el cobro.

La protección se aplica a nuevos permisos de envío. Los intentos antiguos sin
trabajo durable requieren una recuperación operativa explícita. Desplegar
`api-payment` y luego `payment-monitoring` en DEV, validar y repetir en PRD.
Verificar una creación normal y una respuesta perdida simulada en DEV; confirmar
que el segundo caso mantiene un único envío, tiene trabajo durable y activa la
alerta. Una publicación no demuestra recepción efectiva del correo en el buzón.

## Errores de consulta y recuperación durable

El contrato de **Pagos Instantáneos v3** documenta 200, 400, 401, 403, 404 y 500
para creación y consulta. No publica una política completa de backoff ni
documenta 429 en esos endpoints. No usar los tipos `DO_NOT_RETRY` o
`WAIT_4_HOURS_BEFORE_RETRY` de Open Finance: corresponden a otro producto.

| Respuesta o fallo | Tratamiento local |
| --- | --- |
| Red, timeout, 408, 429 o 5xx | Conservar el trabajo y reprogramar la consulta con espera progresiva, acotada a quince minutos en ausencia de `Retry-After`. Alertar después de tres consultas fallidas consecutivas. |
| 400, 401, 403 o 404 | Informar revisión, alertar inmediatamente y conservar una consulta de recuperación cada hora. Nunca interpretar 404 como no pago. |
| JSON inválido, respuesta excesiva o identidad/estado no verificable | Revisión y alerta; no aceptar fondos ni liberar cuotas. Consulta de recuperación cada hora. |
| Cancelación o deadline del llamador | Devolver el error: no consumir el mensaje como si la recuperación se hubiera persistido. |
| Error DynamoDB o transacción fallida | Devolver el error para reintento SQS/DLQ; conservar el trabajo original. |

Estas esperas son decisiones operativas de Indómito, no un SLA de Khipu. Los
reintentos usan transacciones y nuevos trabajos diferidos, por lo que una caída
del proveedor que supere cinco recepciones no agota por sí sola el mensaje en la
DLQ. Los fallos reales de nuestra persistencia siguen usando la DLQ y su alarma.
Una respuesta válida reinicia el contador de fallos consecutivos.

Si llega `Retry-After` en 429 o 503, se admiten segundos enteros o fecha HTTP. La
fecha mínima queda persistida en `providerRetryAt`. Para esperas mayores que los
quince minutos admitidos por SQS, las entregas intermedias solo renuevan el
trabajo: no consultan Khipu ni actualizan la fecha de última consulta. También
pueden cerrar el trabajo si mientras tanto llegó una confirmación verificada.

Cada GET tiene un máximo de diez segundos para dejar tiempo de persistencia
dentro de la Lambda de 29 segundos, incluso cuando la confirmación requiere dos
consultas. La creación conserva su límite existente de quince segundos y **no se
reintenta automáticamente**, ni siquiera ante 429 o 500. Sus errores mantienen
`ErrDispatchUncertain` y el trabajo previo, y añaden clasificación redactada.

Los logs conservan código interno y HTTP status, sin cuerpos de error, URLs de
pago ni credenciales. La métrica `Indomito/Payments/ProviderRecoveryRequired`
activa una alarma adicional en PRD y reutiliza SNS/SMTP existentes. Esta métrica
y alarma están sujetas al costo y a la cuota gratuita disponible de CloudWatch.
La alarma indica un evento que requiere revisión; un cambio de alarma por falta
de muestras no prueba que todos los intentos se hayan recuperado.

Referencias contrastadas:

- [Consulta por ID v3](https://docs.khipu.com/apis/v3/instant-payments/openapi/other/getpaymentbyid)
- [Creación v3](https://docs.khipu.com/apis/v3/instant-payments/openapi/other/postpayment)
- [Retry-After, RFC 9110 sección 10.2.3](https://www.rfc-editor.org/rfc/rfc9110.html#section-10.2.3)
- [429, RFC 6585 sección 4](https://www.rfc-editor.org/rfc/rfc6585.html#section-4)

## Recuperación periódica de trabajos sin mensaje

`fn-recuperar-conciliaciones-v1` se programa cada quince minutos con 128 MB y
timeout de 29 segundos (presupuesto interno de 25 segundos). No consulta Khipu:
solo reenvía trabajos existentes a la misma cola de conciliación.

Cada ejecución realiza una única `Query` consistente sobre
`RECONCILIATION#PENDING`, con rango de sort key hasta cinco minutos antes de la
hora actual y límite de veinte registros. No usa Scan, GSI nuevo ni consultas
por cuentas. Los trabajos recientes, reservados o con `providerRetryAt` futuro
se omiten. El cursor persistido en `RECONCILIATION#RECOVERY/STATE` permite avanzar
aunque una página completa esté esperando legítimamente. Al terminar el rango,
el cursor vuelve al inicio. Un volumen mayor que veinte candidatos requiere
varias ejecuciones, por lo que el tiempo de barrido completo depende de la cola
de trabajos pendientes.

Una concesión CAS de dos minutos evita barridos simultáneos. Cada candidato se
reserva condicionalmente por quince minutos antes de enviarlo. Si el trabajador
lo borró después de la Query, la condición impide recrearlo. Si el envío o la
respuesta del commit se pierden, el trabajo sigue guardado y puede recuperarse
al vencer la concesión. No se garantiza entrega única: SQS puede entregar
duplicados y el consumidor conserva su validación e idempotencia financiera.
Cada nueva generación de trabajo elimina la concesión de rescate anterior.

No se comprueba la presencia individual de un mensaje en SQS. Los cinco minutos
de margen son una heurística conservadora para rescatar trabajos que no avanzan;
un mensaje muy atrasado puede coexistir con el reenvío y será deduplicado por el
consumidor. El recuperador nunca crea cobros ni libera cuotas por antigüedad.
Tampoco consume ni elimina mensajes de la DLQ: si quedó allí una copia de un
trabajo ya recuperado, la alarma de esa cola requiere revisión/redrive operativo.

Se reutilizan cola, DLQ, SNS/SMTP y la métrica/alarma `ProviderRecoveryRequired`.
No se agregan alarmas, métricas personalizadas, índices ni parámetros SSM para
este punto. Los permisos del nuevo rol se limitan a trabajos/cursor y
`sqs:SendMessage`; no incluyen cuentas financieras, secretos Khipu ni S3.

Costo base: unas 2.880 ejecuciones por mes de treinta días, un GetItem de control,
una Query acotada y dos escrituras transaccionales del cursor por ejecución.
Solo los rescates consumen escrituras y envíos adicionales. DynamoDB bajo demanda
cobra esas operaciones; las cuotas gratuitas de Lambda/SQS dependen del consumo
total. Se mantiene el objetivo de costo marginal bajo, sin prometer costo cero.

Las pruebas cubren vacío, límites, exclusión de trabajos futuros/recientes,
Retry-After, concesiones concurrentes, cursor entre páginas, borrado concurrente,
fallo SQS, commit sin respuesta, fallo Query, filas inválidas y pago idempotente.

Despliegue: `api-payment` incorpora función, programación y permisos. Mantener
también publicado `payment-monitoring` con la alarma de los cambios anteriores.
Validar primero DEV y comprobar luego en PRD la regla habilitada y el avance del
cursor. El despliegue no debe provocar pagos nuevos de prueba en producción.

## Permisos del consumidor

El consumidor necesita `GetItem` sobre `RECONCILIATION#PENDING`. Sin ese permiso,
los mensajes agotan cinco recepciones y pasan a la DLQ antes de consultar Khipu.
Los fallos por mensaje se registran y se devuelven como `BatchItemFailures`;
la métrica Lambda Errors por sí sola no representa estos fallos.

## Publicación y recuperación

1. Validar y desplegar `api-payment` en DEV antes de PRD. Verificar el rol del
   consumidor, incluida la lectura de la partición de trabajos.
2. Desplegar `api-payment` en PRD y después `payment-monitoring`: la nueva alarma
   utiliza el output `PaymentReconciliationDLQName` del primer stack. Reutiliza
   SNS y SMTP existentes, y agrega una alarma estándar de CloudWatch solo en PRD
   (revisar la cuota gratuita disponible).
3. Resolver la URL/ARN de la DLQ y de la cola de origen desde los recursos del
   stack del ambiente. Ejecutar un redrive autorizado de la DLQ a su cola de
   origen con velocidad limitada, después de corregir y verificar los permisos.
   No editar saldos, borrar intentos ni crear otro checkout para recuperarlos.
4. Verificar que desaparezca el trabajo anterior, se publique `PROVIDER_STATUS`
   y se cree el siguiente trabajo si Khipu continúa pendiente. Confirmar que la
   DLQ se vacíe y no vuelva a recibir mensajes. Un despliegue por sí solo no
   reproduce los mensajes que ya están en la DLQ.
5. Si el pago está confirmado, comprobar que no se dupliquen ingreso ni
   comprobante. Si sigue pendiente, conservar el bloqueo y los reintentos.

Los estados pendientes se reprograman a 2, 5 y luego 15 minutos. Tras 24 horas
desde la expiración se informa `PROVIDER_REVIEW_REQUIRED`, conservando la consulta
periódica para recibir una confirmación tardía. La expiración aislada no libera
la cuota. La resolución de un pago ambiguo requiere evidencia del proveedor;
no se considera rechazo por el tiempo transcurrido.

Un cobro eliminado desde Khipu puede responder `deleted/pending`. El conciliador
lo resuelve como `UNPAID_FINAL` únicamente si no tiene fecha de conciliación ni
un resultado financiero local previo. Conserva una resolución auditable, libera
el intento abierto y termina el trabajo durable. Un estado eliminado con fondos
registrados o evidencia contradictoria requiere revisión; nunca revierte dinero
por el hecho de eliminar el cobro.

## Validación local

```sh
go test -race ./services/api-payment/functions/collection
go vet ./services/api-payment/... ./services/payment-monitoring/...
STAGE=dev node --require tsx/cjs --test scripts/payment-reconciliation-config.test.cjs
STAGE=prd node --require tsx/cjs --test scripts/payment-reconciliation-config.test.cjs
```
