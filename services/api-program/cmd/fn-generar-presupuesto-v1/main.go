package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	budget "ind-hub-api-gox-sls-pri-gh/services/api-program/functions/generar-presupuesto-v1"
)

func main() {
	lambda.Start(budget.Handle)
}
