package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions"
)

func handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	table, endpoint := os.Getenv("CONVERSATIONS_TABLE_NAME"), os.Getenv("INBOX_WEBSOCKET_MANAGEMENT_ENDPOINT")
	if table == "" || endpoint == "" {
		return events.SQSEventResponse{}, errors.New("realtime configuration is required")
	}
	config, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	gateway := apigatewaymanagementapi.NewFromConfig(config, func(options *apigatewaymanagementapi.Options) { options.BaseEndpoint = &endpoint })
	publisher := functions.RealtimePublisher{DB: dynamodb.NewFromConfig(config), Gateway: gateway, TableName: table, Now: time.Now}
	return functions.ProcessRealtimeQueue(ctx, publisher, event), nil
}

func main() { lambda.Start(handle) }
