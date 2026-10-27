package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	endpoint "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions/obtener-metricas-operacionales-v1"
)

func main() { lambda.Start(endpoint.Handle) }
