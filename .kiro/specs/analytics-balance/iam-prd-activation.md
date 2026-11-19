# Activación del acceso IAM en PRD

El usuario autorizó expresamente crear el acceso de Analítica en PRD después de la entrega local. Esta ejecución solo configura el acceso de aplicación: no despliega frontend, API ni authorizer.

- Perfil AWS: `pa-prd`; cuenta verificada `576394965493`; región `us-east-1`.
- Tabla autoritativa: `ind-prd-usuarios-ddb-prd-pri-use1`, resuelta desde CloudFormation.
- Perfil existente: `ADM`, comprobado antes de escribir.
- Creación transaccional de cuatro registros: LV1 `ANALYTICS`, LV2 `ANALYTICS_BALANCE_DETAIL` y sus dos concesiones a ADM.
- Jerarquía: **Analítica → Detalle balance**, ruta `/analitica/detalle-balance`, endpoint `/analitica`.
- Los 16 registros anteriores de ADM se compararon por clave y contenido y permanecen intactos.
- Durante la verificación se observaron ajustes concurrentes en los registros nuevos: orden del hijo 1, metadatos adicionales y permisos CRUD para ADM. Se conservaron; no se sobrescribieron. También apareció otro registro de perfil ajeno a los cuatro creados por este bootstrap, que no se modificó.
- Verificación final: módulos activos, nombres, jerarquía, ruta, endpoint y permisos requeridos presentes.
- Reejecución idempotente: cero cambios.
- Los usuarios deben renovar los permisos o volver a iniciar sesión. La funcionalidad requiere el despliegue correspondiente para poder usarse en PRD.

## Comando ejecutado

```sh
python3 scripts/configure-analytics-iam-dev.py --stage prd --account-id 576394965493 --apply
```

El nombre del script se conserva por compatibilidad. `--stage` admite dev/prd y conserva DEV por defecto. La cuenta esperada sigue siendo obligatoria. Se corrigió el manejo de respuestas vacías del CLI para registros inexistentes y transacciones exitosas. La comparación idempotente conserva orden visual, metadatos adicionales y concesiones que ya contienen los permisos requeridos.

Registro de creación UTC: `2026-11-19T01:17:52.830177+00:00` (18 de noviembre de 2026, America/Santiago).
