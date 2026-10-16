# Migración y rollback de Inbox

Este documento define los gates operativos. No autoriza despliegues ni cambios de callback.

## Carga inicial

1. Mantener agente, callbacks, writers administrativos y realtime destino apagados.
2. Ejecutar primero `migrate-inbox legacy-to-target --dry-run` con ventana, cuentas, regiones, tablas y buckets explícitos.
3. Exigir cero errores, mismos conteos y hashes canónicos entre origen y modelo neutral.
4. Ejecutar la carga con checkpoint local protegido; reanudar únicamente con los mismos ARN, ventana y hash de configuración.
5. Verificar objetos por tamaño y SHA-256 y luego repetir el manifest neutral contra destino.

Nunca se usa `Scan`: legacy se pagina desde `PK=CHATS` y `PK=CONV#<phone>`; target usa las 16 particiones versionadas.

## Cutover

- Authorizer válido y token alterado denegado.
- SSM completo y agente todavía `enabled=false`.
- Cero `SEND` no terminales y OUTBOX drenado.
- Manifiestos sin diferencias, adjuntos presentes y Calendar validado.
- Smoke HTTP, WebSocket y webhook HMAC correctos antes de habilitar un único escritor.

## Rollback

1. Bloquear UI/agente/envíos y resolver estados `uncertain`.
2. Exportar y verificar journal hasta `rollbackPreseedCutoffSequence`.
3. Preaplicar WAMID, SEND sintetizable y salientes; comprobar índices mediante `GetItem`.
4. Cambiar callback a legacy y verificar handshake/firma sin exponer secretos.
5. Cerrar invocaciones target, fijar cutoff final y aplicar el segundo delta.
6. Verificar hashes, estados monótonos, Calendar, adjuntos y smoke legacy antes de salir de mantenimiento.

Cualquier hueco de secuencia, checksum distinto, objeto ausente o envío no terminal bloquea el corte o rollback.
