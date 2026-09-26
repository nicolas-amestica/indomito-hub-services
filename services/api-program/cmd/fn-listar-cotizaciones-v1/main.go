package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	quotations "ind-hub-api-gox-sls-pri-gh/services/api-program/functions/listar-cotizaciones-v1"
)

func main() {
	lambda.Start(quotations.Handle)
}
