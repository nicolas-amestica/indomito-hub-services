# Checklist de entrega y activación

## Antes de desplegar

- Revisar todos los `git diff` por repositorio y separar cambios ajenos.
- Confirmar SSM SecureString, rotar credenciales previamente compartidas y nunca copiarlas a `.env` versionado.
- Ejecutar Go con race/vet, Angular, infraestructura y typecheck Serverless.
- Compilar frontend administrativo y portal de pagos.
- Compilar `api-payment` desde su manifiesto y exigir correspondencia exacta entre funciones configuradas y ZIP. Evidencia local actual: 54/54, sin duplicados ni entrypoints inexistentes.
- Mantener checkout público deshabilitado hasta completar la verificación DEV.

## Orden DEV

Los comandos y controles detallados están en [deployment-dev-guide.md](deployment-dev-guide.md).

1. Infraestructura: DynamoDB/S3/API Gateway CORS/CDN según cambios efectivos.
2. IAM authorizer y permisos administrativos.
3. API contract y API payment.
4. Trabajador de comprobantes y mappings de Streams/SQS.
5. Frontend administrativo y portal.

No ejecutar migraciones antes de que todos los stacks estén estables.
El trabajador Go `payment-receipts` se compila, valida y despliega con el Makefile común como servicio independiente; no forma parte del stack `api-payment` porque conserva su propio Stream y cola de fallos.

## Verificación DEV obligatoria

- DNS, certificado, CloudFront y CORS de `pagos.dev.girasindomito.cl`.
- Crear/aprobar contrato ficticio, puesta en marcha y consulta RUT+código.
- Pago DemoBank/Khipu, webhook, conciliación, cuota, comprobante, correo y caja.
- Pago simultáneo, respuesta perdida, reversa, baja/alta, descuento, devolución y registro manual.
- Rotación de código e invalidación de una sesión anterior.
- Fallo SMTP controlado, panel de fallos y reintento.
- Migraciones primero en simulación y conciliación cero antes/después.
- UI autenticada y PDFs en tamaños/temas definidos en QA-01.

## Activación

La activación de checkout requiere aprobación explícita después de revisar evidencia. Configurar presupuestos/alarmas, runbook y responsables de conciliación diaria. No habilitar producción reutilizando credenciales, datos o endpoints DEV.

## Recuperación

Ante incertidumbre monetaria: deshabilitar checkout, conservar webhooks/inbox, no borrar filas, conciliar proveedor y banco, reanudar con la misma clave idempotente y registrar resolución. Un rollback de código no revierte transacciones financieras ya confirmadas.
