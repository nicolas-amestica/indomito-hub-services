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

func handle(ctx context.Context, event events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
	table := os.Getenv("CONVERSATIONS_TABLE_NAME")
	if table == "" {
		return events.APIGatewayProxyResponse{}, errors.New("conversations table is required")
	}
	config, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return events.APIGatewayProxyResponse{}, err
	}
	service, err := domain.NewWebSocketConnectionService(dynamodb.NewFromConfig(config), table, nil)
	if err != nil {
		return events.APIGatewayProxyResponse{}, err
	}
	return functions.ConnectWebSocket(ctx, service, event)
}

func main() { lambda.Start(handle) }
