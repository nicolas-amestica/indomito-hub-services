package main

import (
	"context"

	"github.com/aws/aws-lambda-go/lambda"

	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/iam-auth/functions"
	authorizev1 "ind-hub-api-gox-sls-pri-gh/services/iam-auth/functions/authorize-v1"
)

// Punto de entrada del binario Lambda para el authorizer.
func main() {
	ctx := context.Background()
	app := bootstrap.MustNew(ctx)
	_ = functions.LoadConfig(app)
	lambda.Start(authorizev1.Handle)
}
