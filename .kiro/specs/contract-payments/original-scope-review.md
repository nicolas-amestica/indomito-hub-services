# Revisión final del alcance original

## Cubierto localmente

- Cotización conserva el valor individual por persona sin dividir por cuotas inexistentes.
- Contrato calcula la cuota individual mensual, persiste el valor y lo expresa en formulario/PDF.
- Fecha de viaje opcional y texto “fecha por definir”.
- Día/mes/año de inicio, vencimientos concretos y ajuste de meses cortos.
- Abono no reembolsable y devolución evaluada sobre cuotas posteriores, con redacción contractual versionada.
- Nómina financiera separada, liberados identificados en puesta en marcha, altas/bajas/reemplazos mediante anexos y cuotas que no disminuyen automáticamente.
- RUT+código por gira sin OTP, sesiones breves, pagos completos, manuales, descuentos, devoluciones y conciliación.
- Comprobantes privados descargables/reenviables, identidad v2, fallos y backfill histórico.
- Caja por gira/consolidada, Khipu en tránsito, comisiones, proveedores, alertas y fondos sin aplicar.
- Fronteras futuras BI/DTE y auditoría de costo/seguridad.

## Evidencia local

- API contract con race: correcta.
- 46 archivos y 246 pruebas Angular de contratos/cotizaciones: correctos.
- API payment completa con race, worker, infraestructura, builds y suites indicadas en `qa-evidence.md`: correctos.

## No cerrado sin AWS o decisión comercial

- Validación visual autenticada y render manual representativo.
- Despliegue, IAM/SSM efectivos, DNS/CORS real y recorrido DemoBank completo.
- Tarifas actuales y selección definitiva del proveedor de pagos; deben actualizarse desde fuentes oficiales al decidir.
- Certificación SII, facturador, BI desplegado y asesoría contable/tributaria.
