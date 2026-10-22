package main

import (
	"context"
	"github.com/aws/aws-lambda-go/lambda"
	"ind-hub-api-gox-sls-pri-gh/services/payment-monitoring/functions"
)

func main() {
	lambda.Start(func(ctx context.Context) error {
		app, err := functions.New(ctx)
		if err != nil {
			return err
		}
		return app.Monitor(ctx)
	})
}
