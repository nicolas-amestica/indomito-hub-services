package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	servicecatalog "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/catalogo-servicios"
)

func main() { lambda.Start(servicecatalog.HandleUpdate) }
