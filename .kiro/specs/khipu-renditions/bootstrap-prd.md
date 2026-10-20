# Captura autenticada de arranque PRD

## Propósito y límites

Obtener el destinatario exacto del primer reporte firmado sin conocerlo de antemano.
No es OBSERVE ni APPLY: no crea inbox, trabajos, pagos ni caja. La captura tiene
un rol propio sin DynamoDB y usa el bucket privado de comprobantes existente,
con cifrado AES256 y un prefijo exclusivo. No crea otro bucket ni tareas recurrentes.

El validador reutiliza el formato gzip/JWS RS512. Con `x5c` exige cadena CA
confiable, vigencia y SAN `khipu.com`; sin `x5c` requiere el certificado público
aprovisionado por un canal confiable en `certificatePem`. No descarga claves
desde el cuerpo ni usa la excepción DEV. Referencia:
[Khipu: validación de rendiciones](https://docs.khipu.com/payment-solutions/instant-payments/payment-advanced).

La firma autentica a Khipu, **no demuestra que la cuenta sea la de Indómito**.
La evidencia lleva `AUTHENTICATED_DESTINATION_UNVERIFIED`. Comparar los datos
con la cuenta bancaria registrada en Khipu/cartola antes de copiarlos al config normal.

Solo conserva **un reporte por ventana**, máximo 4 MiB comprimido y descomprimido.
Un duplicado byte a byte devuelve 200 sin sobrescribir. Otro reporte devuelve 503;
no se acusa recepción de contenido descartado. La ventana admite hasta 72 horas
restantes. Este mecanismo debe desactivarse al obtener la muestra.

## Despliegue manual (no ejecutado por el agente)

1. En infrastructure, revisar `environments/.env.prd`. Ya se agregó
   `SSM_PAYMENTS_KHIPU_RENDITIONS_BOOTSTRAP_CONFIG` con `enabled:true`,
   `until:"2026-10-23T00:00:00Z"` y el PEM público oficial de Khipu.
   No hace falta que completes destinatario ni certificado para esta captura.

   Vence el 22 de octubre de 2026 a las 21:00 en Chile continental. Si cambias
   la fecha, usa UTC y no más de 72 horas futuras. JSON en una línea sin comillas
   exteriores. No necesita `recipientUid`, `bank` ni `account`.
   No modifica `SSM_PAYMENTS_KHIPU_RENDITIONS_CONFIG`.

   Certificado obtenido el 20/10/2026 desde el enlace HTTPS publicado por Khipu
   (`https://s3.amazonaws.com/static.khipu.com/certs/khipu_signer.pem`), vigente
   del 27/02/2026 al 30/03/2027. Huella SHA-256:
   `69:BF:54:38:F1:0A:5A:37:E7:26:08:93:9E:83:C6:A5:A0:79:D5:73:78:5D:9B:F5:6B:50:5F:8A:47:8F:02:F9`.
   Es una clave pública, nunca una clave privada. Si se renueva, actualizarla
   desde la fuente oficial; no sustituirla con el certificado de un aviso fallido.

2. Publicar SSM desde infrastructure, como el resto de variables:

   ```bash
   (
     unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_SECURITY_TOKEN
     export AWS_PROFILE=pa-prd
     make deploy resource=ssm stage=prd region=us-east-1
   )
   ```

   Este script procesa todos los parámetros configurados: revisar cambios de
   `.env.prd` antes de ejecutarlo. No publicar un certificado tomado de DEV.

3. Desde la raíz del repositorio API, desplegar la versión nueva con build:

   ```bash
   (
     unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_SECURITY_TOKEN
     export AWS_PROFILE=pa-prd
     make deploy service=services/api-payment stage=prd region=us-east-1
   )
   ```

   No reutilizar el ZIP empaquetado antes de este cambio. Confirmar éxito de
   CloudFormation; un `package` exitoso no equivale a despliegue.

4. Antes de cambiar Khipu, verificar en PRD:

   - Función `fn-capturar-rendicion-khipu-v1` y ruta POST desplegadas.
   - SSM bootstrap-config es SecureString y la ventana está vigente.
   - Rol propio: solo logs/trazas, lectura SSM bootstrap-config, S3 Get/Put del prefijo
     `rendition-bootstrap/prd/*`; sin DynamoDB.
   - Bucket con Block Public Access, cifrado y política TLS activa.
   - Un POST inválido pequeño devuelve 400 y no crea objetos; 503 indica que
     falta resolver configuración/ventana/entorno. Este smoke test no demuestra
     que Khipu haya enviado un reporte ni prueba la escritura S3.

5. En la **cuenta Khipu productiva**, cambiar temporalmente solo
   “URL para recibir el reporte diario de rendición” a:

   ```text
   https://<HOST-REAL-DEL-API-PRD>/pagos/khipu/rendiciones/capturas
   ```

   Tomar el host del despliegue PRD; no copiar el API ID de DEV de conversaciones
   anteriores. Mantener el formato DRN-2.0 que se está probando; confirmar contra
   el mensaje real, pues la documentación pública denomina 3.0 al formato JWS.
   **No cambiar la notificación instantánea de pagos.**

6. Realizar el pago real voluntario desde el portal PRD, después de verificar lo
   anterior. Un pago no garantiza una rendición esa noche; no realizar pagos
   adicionales solo porque aún no aparezca un reporte.

## Revisión de evidencia y transición

La clave es `rendition-bootstrap/prd/<until-en-segundos-Unix>/report.json` en el
bucket de comprobantes PRD. Usar acceso AWS autorizado (no URL pública).
Revisar `trust`, `sha256`, `receivedAt`, y `report` sin copiar datos personales
ni números bancarios a logs/chats. `bodyBase64` conserva el gzip original para
reprocesarlo posteriormente; comprobar su SHA-256 contra `sha256`.

Asignar los valores exactos, conservando ceros iniciales:

| Campo config normal | Campo de `report` |
| --- | --- |
| `recipientUid` | `recipientUid` |
| `bank` | `recipientBankName` |
| `account` | `recipientBankAccountNumber` |

Comparar banco/cuenta con la cuenta registrada en Khipu y confirmar la asociación
del identificador. No asumir que `recipientUid` es RUT o receiver-id.

Luego, manualmente:

1. Completar el JSON normal en `.env.prd` con destino verificado, manteniendo
   `policy.mode = OBSERVE`. Si no hay `x5c`, aprovisionar el certificado público
   oficial vigente en ambos configs. No completar con datos inferidos.
2. Publicar SSM y confirmar configuración vigente del receptor normal.
3. Restituir la URL de rendición normal `/pagos/khipu/rendiciones` en Khipu.
4. Reenviar una vez el gzip original a la ruta normal y verificar 200 + inbox
   OBSERVED. La captura ya devolvió 200: **no asumir que Khipu lo reenviará**.
   El procedimiento debe respetar la validación estricta; no alterar el JWS.
5. Configurar `enabled:false` en bootstrap-config mediante `.env.prd` y SSM.
   Revisar el período temporal en Khipu y recuperar otros reportes si existen.
6. Mantener APPLY deshabilitado hasta validar importes, comisiones, correspondencia
   de pagos y efecto en caja según el runbook general.

La caducidad detiene la recepción, no borra evidencia: el bucket es versionado y
sin expiración automática. Acordar su retención/eliminación después de validar
la transición; no aplicar una regla global que elimine comprobantes.

## Si no llega o se rechaza

- 400: formato/firma/certificado/esquema inválido; no hay evidencia persistida.
  Si Khipu no incluye `x5c`, obtener su PEM público oficial por canal confiable
  y actualizar `certificatePem`. Nunca desactivar validación en PRD.
- 503: configuración/ventana, almacenamiento o ventana ya ocupada; revisar
  evidencia y salud de AWS. No hay acuse de recepción exitosa.
- Sin invocaciones: revisar cuenta productiva, URL, versión y disponibilidad
  real de la rendición con Khipu; no concluir que el pago falló.

Las verificaciones locales no prueban entrega real ni recursos desplegados.
