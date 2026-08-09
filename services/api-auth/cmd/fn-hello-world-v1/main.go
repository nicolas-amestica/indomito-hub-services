package main

import (
	"context"

	"github.com/aws/aws-lambda-go/lambda"

	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-auth/functions"
	helloworldv1 "ind-hub-api-gox-sls-pri-gh/services/api-auth/functions/hello-world-v1"
)

// Punto de entrada del binario Lambda.
func main() {
	ctx := context.Background()
	app := bootstrap.MustNew(ctx)
	_ = functions.LoadConfig(app)
	lambda.Start(helloworldv1.Handle)
}
