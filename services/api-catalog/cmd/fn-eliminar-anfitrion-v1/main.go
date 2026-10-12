package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	meetinghosts "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/anfitriones"
)

func main() { lambda.Start(meetinghosts.HandleDelete) }
