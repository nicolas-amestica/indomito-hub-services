package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	taxsettings "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/actualizar-configuracion-tributaria-v1"
)

func main() { lambda.Start(taxsettings.Handle) }
