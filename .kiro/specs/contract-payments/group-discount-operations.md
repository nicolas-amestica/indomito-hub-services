# Descuentos grupales extraordinarios

## Flujo

1. Administración selecciona todos los pagantes activos o una lista explícita, porcentaje y fundamento.
2. El servidor bloquea brevemente la gira, congela versiones y calcula el descuento sobre cada cuota pendiente. El borrador no modifica deuda.
3. Otra confirmación, con auditoría propia, aprueba el impacto total.
4. La gira se bloquea y todas las cuentas se revalidan antes del primer efecto. Un intento abierto, una baja o cualquier cambio de versión rechaza el borrador completo.
5. Cada cuenta se actualiza idempotentemente; la operación solo se publica `APPLIED` al completar todas las seleccionadas.

Las altas posteriores nunca heredan el descuento. El porcentaje se aplica al saldo exigible congelado, con redondeo hacia abajo por cuota en pesos enteros.

## Recuperación

El ID público queda en `?descuento=`. Estados `VALIDATING` y `APPLYING` se reanudan con la misma aprobación. `REJECTED` no se reutiliza: se crea un borrador nuevo después de resolver el intento abierto o revisar los cambios.
