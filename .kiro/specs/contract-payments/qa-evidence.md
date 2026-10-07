# Evidencia QA local

## Empaquetado de API payment

- `make build service=services/api-payment`: correcto.
- Validación estricta del manifiesto: 54 funciones, 54 artefactos ZIP, sin nombres duplicados, sin faltantes/sobrantes y con todos los entrypoints presentes.
- Alcance: evidencia local de compilación y empaquetado; no demuestra creación de Lambdas, permisos ni rutas en AWS.
- La recompilación posterior a incorporar las barreras de duplicados volvió a finalizar correctamente.
- `make validate ... stage=dev` ya selecciona el perfil correcto y descarta credenciales ambientales vencidas. Tras renovar SSO, la ejecución llegó correctamente a CloudFormation y se detuvo porque el stack S3 desplegado aún no publica `ReceiptsBucketName`; esto confirma el orden de prerrequisitos documentado.

## Auditoría AWS DEV de solo lectura

- Identidad confirmada en la cuenta DEV documentada mediante rol SSO administrativo.
- `pagos.dev.girasindomito.cl` resuelve a la distribución CloudFront esperada y responde HTTPS 200.
- La tabla de pagos está ACTIVE, pero el stack desplegado todavía no habilita DynamoDB Stream; la plantilla local empaquetada sí declara `NEW_IMAGE` y `PagosTableStreamArn`.
- El stack S3 desplegado no contiene el bucket privado de comprobantes; la plantilla local empaquetada sí incluye bucket cifrado, versionado, bloqueo público, política TLS y outputs Name/Arn.
- SSM desplegado contiene los tres parámetros Khipu. Faltan sesión pública, lookup, URL del portal y SMTP; los tres primeros están configurados localmente y requieren redespliegue, SMTP sigue sin valor local.
- API Gateway mantiene solo seis rutas históricas de pagos. El preflight devuelve 204, pero no autoriza todavía `http://localhost:4400` ni `https://pagos.dev.girasindomito.cl`; la corrección existe únicamente en la plantilla local.
- Infraestructura DDB, S3, API Gateway y payments CDN empaquetada localmente sin errores. No se desplegó ningún recurso.

## Infraestructura DEV después del despliegue del usuario

- SSM, DDB, S3 y API Gateway están `UPDATE_COMPLETE`.
- Existen siete parámetros de pagos/autorización como `SecureString`; no se leyeron valores.
- Tabla de pagos ACTIVE con Stream `NEW_IMAGE` y ARN publicado.
- Bucket de comprobantes con AES256, versionado y los cuatro bloqueos de acceso público activos.
- Preflight efectivo autoriza `http://localhost:4400`, `https://pagos.dev.girasindomito.cl` y `https://nuevo.admin.dev.girasindomito.cl`.
- `api-payment`: build de 54 ZIP, validación Serverless de 15 checks y package correctos.
- `api-contract`: se corrigieron cuatro anexos ausentes de su manifiesto; build de 11 ZIP, validación de 15 checks y package correctos.
- IAM: typecheck, lint y package correctos.
- Worker de comprobantes migrado a Go: race/vet, build, 15 checks y package correctos; template con Lambda ARM64 `provided.al2023`, event source mapping y cola SQS.
- Ambos frontends compilan. Se eliminó `/tesoreria` del portal público y una prueba impide volver a publicar rutas administrativas allí; el nuevo bundle ya no contiene el chunk financiero.

Fecha: 2026-10-05. No incluye despliegue ni recorrido autenticado en AWS.

## Correcto

- `go test -race ./services/api-payment/...`: dominio, handlers, persistencia, proveedores y comandos correctos.
- Trabajador de comprobantes: pruebas correctas, incluidos PDF v3 con QR, compatibilidad histórica, backfill sin correo, fallos y reintentos.
- TypeScript Serverless compila con `tsc --noEmit --skipLibCheck`.
- Collections Angular: 21 archivos y 42 pruebas correctas.
- Build del portal de pagos DEV correcto.
- Build de administración DEV correcto.
- Esquemas JSON de BI y DTE parsean correctamente.
- Portal local inspeccionado realmente en Chrome a 320, 390, 768 y 1440 px: sin overflow horizontal, campos con etiquetas, botones visibles y enlace de salto al contenido.
- Portal local inspeccionado visualmente a 320 px en temas oscuro y claro: jerarquía, textos, controles y advertencias permanecen legibles.
- Se generaron y renderizaron con Ghostscript artefactos ficticios estables. Las ocho páginas del contrato no presentan recortes, superposiciones ni tablas fuera de margen; “fecha por definir” es legible y la cláusula económica expresa $600.000 grupales y $20.000 mensuales por pasajero pagante.
- El comprobante v3 de una página presenta nombre, RUT formateado, monto, concepto, advertencia de que no es boleta/factura SII y QR/código de verificación, sin cortes ni elementos solapados.

## Pendiente de evidencia visual

- Administración autenticada a 320, 390, tablet y escritorio, en claro/oscuro; no se falsificó una sesión administrativa local.
- Navegación con teclado, foco, mensajes y tablas con scroll en todas las páginas nuevas.
- Render de contrato con código de portal y anexo contractual plenamente poblados; el contrato ficticio revisado cubre fecha indefinida y cuota individual, pero usa marcadores esperables en los datos no suministrados.
- Render visual del anexo contractual con datos ficticios.
- Recorrido desplegado portal → DemoBank/Khipu → webhook → cuota → comprobante → caja.

Estas verificaciones no se marcan aprobadas por compilación o tests unitarios.
