package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	"ind-hub-api-gox-sls-pri-gh/services/api-identity/functions"
)

func main() { lambda.Start(functions.Handle) }
