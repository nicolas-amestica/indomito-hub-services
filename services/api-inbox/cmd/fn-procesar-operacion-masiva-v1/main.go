package main

import (
	"context"
	"errors"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions"
)

func handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	table := os.Getenv("CONVERSATIONS_TABLE_NAME")
	if table == "" {
		return events.SQSEventResponse{}, errors.New("conversations table is required")
	}
	config, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	worker, err := domain.NewBulkWorker(dynamodb.NewFromConfig(config), table, nil)
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	return functions.ProcessBulkQueue(ctx, worker, event), nil
}

func main() { lambda.Start(handle) }
