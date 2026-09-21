package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	catalogs "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/obtener-catalogos-v1"
)

func main() {
	lambda.Start(catalogs.Handle)
}
