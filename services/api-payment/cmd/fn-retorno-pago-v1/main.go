package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/cloud"
)

func main() { lambda.Start(cloud.Handler(cloud.Return)) }
