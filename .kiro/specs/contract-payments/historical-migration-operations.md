# Migración histórica de cobranza

## Regla principal

No existe una migración global implícita. El operador debe entregar un manifiesto de giras y cuentas conocidas; el sistema nunca usa `Scan` para descubrirlas ni interpreta contratos antiguos como dinero recibido.

## Inventario previo

Por cada gira se debe conciliar:

1. contrato aprobado, versión e instantánea `SETUP`;
2. cantidad de cuentas, pagantes y liberados;
3. presencia y versión de `TRIP/MEMBER`;
4. suma de abono pactado, cuotas originales, descuentos, pagos, fondos sin aplicar, devoluciones y saldo;
5. cantidad de eventos, comprobantes v1/v2 e intentos abiertos o en revisión;
6. movimientos bancarios, liquidaciones Khipu y compromisos de proveedores.

Una diferencia bloquea la aplicación para esa gira; no se corrige automáticamente.

## Orden reanudable

1. Ejecutar la migración de nómina en modo de preparación y completar `rosterSchemaVersion=1`.
2. Consultar nuevamente cuentas y saldos; deben ser idénticos al inventario previo.
3. Simular el backfill de comprobantes cuenta por cuenta (`apply=false`) y guardar conteos/cursor.
4. Aplicar páginas de diez y reanudar con el cursor. El modo `DOCUMENT_ONLY` evita correos históricos.
5. Esperar que los trabajos queden `DOCUMENT_READY`; reconciliar cantidad, SHA y rutas v2.
6. Comparar nuevamente todos los totales financieros. Contrato, eventos, diario y caja deben conservar sus huellas.

## Rollback y evidencia

Las filas de nómina se publican solo al terminar. El backfill vigente genera PDF v3 verificables como objetos nuevos; los v1/v2 no se eliminan. Si una página falla, se reintenta con la misma clave. No se debe borrar una versión ni forzar estados DynamoDB manualmente. La evidencia mínima es el manifiesto, simulaciones, conteos antes/después, comandos, cursores y diferencias igual a cero.

## Límites

- Contratos que no satisfacen las condiciones nuevas quedan históricos; no se aprueban ni transforman.
- Confirmaciones sin referencia verificable permanecen en revisión.
- Registros antiguos sin proyección `ACCOUNT#/RECEIPT#` requieren un manifiesto específico y revisión humana; no se buscarán mediante exploración global.
- La ejecución real y la validación AWS pertenecen a AWS-01/RELEASE-01 y no se realizaron localmente.
