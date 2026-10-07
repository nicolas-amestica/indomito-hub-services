package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	c "ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/collection"
)

func main() { lambda.Start(c.OperationRecoveryHandler) }
