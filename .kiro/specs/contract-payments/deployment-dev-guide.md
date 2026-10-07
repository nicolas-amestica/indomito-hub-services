# Guía de despliegue DEV del sistema de pagos

Esta guía deja el despliegue en manos del operador. Los comandos no habilitan por sí solos cobros reales: el checkout público debe permanecer deshabilitado hasta completar la verificación funcional.

## 1. Preparación

Usar el perfil AWS `pa-dev`, región `us-east-1` y una copia de trabajo revisada. No incorporar archivos `.env`, credenciales ni artefactos locales al control de versiones.

Antes de modificar AWS:

```bash
aws sts get-caller-identity --profile pa-dev
```

Si la sesión está expirada, ejecutar `aws sso login --profile pa-dev` y repetir la consulta. La cuenta dev esperada debe coincidir con la documentación interna. Detenerse si la identidad no corresponde.

## 2. Infraestructura

Desde `ind-hub-inf-aws-sls-pri-gh`:

```bash
npm test
npm run typecheck
make deploy resource=ssm stage=dev region=us-east-1
make deploy resource=ddb stage=dev region=us-east-1
make deploy resource=s3 stage=dev region=us-east-1
make deploy resource=payments-cdn stage=dev region=us-east-1
make deploy resource=api-gateway stage=dev region=us-east-1
```

La auditoría del 2026-10-05 encontró que AWS solo contiene los tres parámetros Khipu. Aunque el archivo local ya define sesión, URL y lookup, es necesario volver a desplegar SSM. `SSM_PAYMENTS_RECEIPT_SMTP` todavía falta en `environments/.env.dev`; debe completarse antes del despliegue si se quiere probar entrega por correo. Nunca imprimir valores secretos para comprobarlos.

El valor SMTP es un JSON en una sola línea con esta forma (reemplazar todos los ejemplos):

```dotenv
SSM_PAYMENTS_RECEIPT_SMTP={"host":"smtp.example.test","port":587,"user":"usuario","password":"secreto","from":"Giras Indomito <pagos@example.test>"}
```

Solo se aceptan los puertos 465 o 587; el trabajador exige TLS y validación de certificado.

También se comprobó que el stack desplegado aún no tiene `PagosTableStreamArn` ni `ReceiptsBucketName/Arn`; por eso DDB y S3 son prerrequisitos obligatorios antes de API payment y del trabajador. Esperar que DynamoDB, S3, CloudFront y API Gateway terminen estables antes de continuar.

## 3. Autorización administrativa

Desde `ind-hub-iam-tsx-sls-pri-gh`:

```bash
npm run typecheck
npm run lint
npm run deploy:dev
```

Estado local previo: typecheck, lint y package correctos.

Después, ejecutar el chequeo DEV disponible sin copiar tokens al historial:

```bash
npx tsx scripts/payment-dev-check.ts
```

## 4. Contratos y pagos

Desde `ind-hub-api-gox-sls-pri-gh`:

```bash
go test -race ./services/api-contract/...
go test -race ./services/api-payment/...
npx tsc --noEmit --pretty false
make deploy service=services/api-contract stage=dev region=us-east-1
make deploy service=services/api-payment stage=dev region=us-east-1
```

El despliegue de `api-payment` vuelve a compilar y validar el manifiesto. Debe informar 15 checks correctos; la validación V15 exige que Serverless y `service.config.json` contengan exactamente las mismas funciones.

Estado local previo: `api-contract` empaqueta 11 Lambdas y `api-payment` 54; ambos pasan los 15 checks.

## 5. Trabajador de comprobantes

Desde `ind-hub-api-gox-sls-pri-gh/services/payment-receipts`:

```bash
go test -race ./...
go vet ./...
cd ../..
make deploy service=services/payment-receipts stage=dev region=us-east-1
```

Este servicio Go usa el mismo build/validate/deploy del resto del backend, pero mantiene un stack independiente para su Stream y cola SQS.

Estado local previo: race, vet, 15 checks y package correctos; el template contiene una Lambda ARM64 `provided.al2023`, un mapping de Stream y una cola de fallos SQS.

## 6. Frontends

Desde `ind-hub-app-ngx-pri-gh`:

```bash
npm test -- --watch=false
npm run build:dev
npm run build:payments:dev
npm run deploy:dev
npm run deploy:payments:dev
```

El portal debe quedar asociado a `pagos.dev.girasindomito.cl`; no cambiar DNS hasta que el stack `payments-cdn` entregue el destino esperado.

## 7. Verificación y activación

Verificar en este orden:

1. CORS y carga del portal desde el dominio DEV.
2. Contrato ficticio aprobado, puesta en marcha y liberados identificados.
3. Consulta con RUT ficticio y código de viaje.
4. Pago DemoBank, webhook durable, conciliación, cuota pagada, comprobante S3/correo y movimiento de caja.
5. Doble intento simultáneo, respuesta perdida, pago manual, descuento, baja/alta, devolución y reversa.
6. Rotación/revocación del código y caducidad de la sesión anterior.
7. Fallo SMTP, panel administrativo y reintento.
8. Migraciones en modo simulación, con conciliación monetaria sin diferencias antes de aplicar.

Solo después de conservar evidencia de todo el recorrido se puede solicitar la activación explícita del checkout. No realizar pruebas con RUT, correos, cuentas bancarias ni dinero reales.

## 8. Recuperación

Si aparece una diferencia monetaria, deshabilitar checkout sin apagar la recepción de webhooks. No borrar ni editar filas financieras: conciliar proveedor y banco, resolver o reanudar usando la misma clave idempotente y documentar el resultado. Un rollback de código no deshace asientos ya confirmados.
