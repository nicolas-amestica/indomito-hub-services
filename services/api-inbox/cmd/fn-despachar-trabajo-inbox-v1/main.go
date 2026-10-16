package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions"
)

func handle(ctx context.Context, event events.DynamoDBEvent) (events.DynamoDBEventResponse, error) {
	inboundURL, workflowURL, realtimeURL, agentURL := os.Getenv("INBOX_INBOUND_QUEUE_URL"), os.Getenv("INBOX_WORKFLOW_QUEUE_URL"), os.Getenv("INBOX_REALTIME_QUEUE_URL"), os.Getenv("INBOX_AGENT_QUEUE_URL")
	if inboundURL == "" || workflowURL == "" || realtimeURL == "" || agentURL == "" {
		return events.DynamoDBEventResponse{}, errors.New("inbox queue URLs are required")
	}
	config, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return events.DynamoDBEventResponse{}, err
	}
	return (functions.WorkDispatcher{Queue: sqs.NewFromConfig(config), InboundURL: inboundURL, WorkflowURL: workflowURL, RealtimeURL: realtimeURL, AgentURL: agentURL, Now: time.Now}).Handle(ctx, event), nil
}

func main() { lambda.Start(handle) }
