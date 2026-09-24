package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	"ind-hub-api-gox-sls-pri-gh/services/api-contract/functions"
)

func main() { lambda.Start(functions.HandleList) }
