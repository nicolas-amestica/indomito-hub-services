package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	gettaxsettings "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/obtener-configuracion-tributaria-v1"
)

func main() { lambda.Start(gettaxsettings.Handle) }
