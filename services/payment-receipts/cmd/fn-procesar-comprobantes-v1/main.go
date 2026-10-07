package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	"ind-hub-api-gox-sls-pri-gh/services/payment-receipts/functions"
)

func main() { lambda.Start(functions.Handler) }
