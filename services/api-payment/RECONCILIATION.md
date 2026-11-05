# Recuperación de conciliación Khipu

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

## Validación local

```sh
go test -race ./services/api-payment/functions/collection
go vet ./services/api-payment/... ./services/payment-monitoring/...
STAGE=dev node --require tsx/cjs --test scripts/payment-reconciliation-config.test.cjs
STAGE=prd node --require tsx/cjs --test scripts/payment-reconciliation-config.test.cjs
```
