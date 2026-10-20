# Observación limitada DEV — 2026-10-19

**Histórico:** el usuario autorizó posteriormente una excepción permanente para futuros reportes DEV. Ver `permanent-dev.md`. Los límites y vencimiento de esta página corresponden únicamente al procesador diagnóstico anterior.

## Alcance autorizado

Herramienta manual `services/api-payment/cmd/observe-rendition-dev`, separada del webhook y del verificador financiero estricto. No se desplegó el servicio financiero ni se activó APPLY. No modifica producción ni la notificación instantánea de pagos.

La excepción admite exclusivamente la cuenta AWS DEV `382670112717`, modo OBSERVE, una huella de certificado y el hash exacto de la muestra capturada. No acepta reportes futuros automáticamente. Se omiten confianza X.509, vigencia y SAN únicamente en este procesador; se sigue verificando la firma matemática RS512 y el esquema. Esto NO autentica al proveedor.

- Configuración: SecureString `/indomito/dev/payments/khipu/renditions/dev-observation-config`, versión 1.
- Caducidad: `2026-10-20T13:48:48Z` (20 de octubre, 10:48:48 de Chile), sin renovación automática; ventana máxima admitida de 72 horas.
- Certificado SHA-256: `fe843f343a83682dc35b50090ccf6196266da681edbfd5111e1c29805af46f83`.
- Reporte comprimido SHA-256: `94ad368e9765206caa396e74bcdfb08fa461b92d6f1648870f32453c773be35a`.
- Entrada fija: bucket privado de captura DEV, `unverified/slot-01.json`.
- El destinatario se compara con la muestra aprobada; no constituye verificación independiente de la cuenta bancaria.

## Ejecución

Desde `services/api-payment`, con sesión `pa-dev` vigente:

```sh
go run ./cmd/observe-rendition-dev
```

El programa comprueba la cuenta mediante STS antes de leer configuración/evidencia. El adaptador DynamoDB expone solo GetItem consistente; no dispone de operaciones de escritura. La única escritura del procesador es un resumen sin datos personales en S3, bajo `unverified/observations/<hash>.json`, mediante creación condicional. Un replay idéntico no crea otra observación; un resumen diferente falla sin sobrescribir.

No se creó un rol IAM nuevo para el operador: se usa su perfil DEV. La restricción de solo lectura DynamoDB descrita corresponde al código del procesador, no a una reducción de los permisos del perfil.

## Resultado inicial comprobado en AWS

- Tres coincidencias, cero casos en revisión, total bruto CLP 104.245.
- Montos: 34.748, 34.748 y 34.749. Comisiones inmediatas y diferidas: cero en esta muestra developer, sin extrapolar a producción.
- Coinciden intentos, eventos PAYMENT_RECEIVED, referencias, cuentas y liquidaciones pendientes; `settledGross` permanece en cero en los tres registros leídos.
- Cero escrituras de caja; primera ejecución `duplicate:false`.
- Segunda ejecución contra AWS: `duplicate:true`, mismas tres coincidencias y liquidaciones aún en cero. ListObjectsV2 confirmó un único resumen de observación (1.237 bytes).
- `fundsRenditionDate` coincide con paymentDate en cada ítem. No se interpreta como fecha bancaria verificada.
- Evidencia etiquetada `DEV_PINNED_CERT_EXCEPTION_UNTRUSTED`, `providerAuthenticated:false`.

La observación no entra al inbox financiero ni aparece automáticamente en su UI. Los objetos mantienen el lifecycle de siete días del bucket de captura.

## Validaciones y siguientes pasos

`go test ./...` y `go vet ./...` del módulo pasan. Pruebas race del nuevo paquete pasan, cobertura 81,7%; lint acotado sin diagnósticos. Incluyen aislamiento por ambiente/cuenta, caducidad, pins, firma alterada, restricciones de política, replay y ausencia de escrituras DynamoDB. Persisten diagnósticos globales preexistentes documentados en tasks.md.

Actualización 2026-10-19: prueba financiera controlada autorizada y completada; ver `financial-dev-test.md`. Los valores en cero de la observación anterior describen el estado previo: las tres liquidaciones DEV ahora están conciliadas como prueba. No utilizar esta excepción como fallback productivo. Antes de producción, validar firma/cadena oficiales y contrastar destinatario, fecha bancaria y comisiones con evidencia independiente.
