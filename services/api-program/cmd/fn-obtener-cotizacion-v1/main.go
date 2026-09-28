package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	quotation "ind-hub-api-gox-sls-pri-gh/services/api-program/functions/obtener-cotizacion-v1"
)

func main() {
	lambda.Start(quotation.Handle)
}
