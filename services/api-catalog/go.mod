module ind-hub-api-gox-sls-pri-gh/services/api-catalog

go 1.25.0

require (
	github.com/aws/aws-lambda-go v1.48.0
	github.com/aws/aws-sdk-go-v2 v1.47.0
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.21.4
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.68.0
	github.com/labstack/echo/v4 v4.15.4
	go.uber.org/zap v1.28.0
	ind-hub-api-gox-sls-pri-gh v0.0.0
	ind-hub-api-gox-sls-pri-gh/libs v0.0.0
)

// El repo es un Go workspace (ver go.work): estos dos modulos se resuelven
// desde el disco, no desde un proxy. Las directivas replace los dejan tambien
// resolubles fuera del workspace, para no depender de que toda herramienta que
// toque este modulo lo cargue con go.work activo.
replace (
	ind-hub-api-gox-sls-pri-gh => ../..
	ind-hub-api-gox-sls-pri-gh/libs => ../../libs
)

require (
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.41.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.13.3 // indirect
	github.com/aws/smithy-go v1.28.1 // indirect
	github.com/labstack/gommon v0.5.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)
