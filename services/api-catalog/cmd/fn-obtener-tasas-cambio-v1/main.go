package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	rates "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/obtener-tasas-cambio-v1"
)

func main() {
	lambda.Start(rates.Handle)
}
