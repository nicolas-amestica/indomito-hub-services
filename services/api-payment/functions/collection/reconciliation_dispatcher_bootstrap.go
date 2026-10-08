package collection

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
)

// ReconciliationDispatcherHandler conecta DynamoDB Streams con la cola diferida.
func ReconciliationDispatcherHandler(ctx context.Context, event events.DynamoDBEvent) (events.DynamoDBEventResponse, error) {
	queueURL := os.Getenv("PAYMENT_RECONCILIATION_QUEUE_URL")
	if queueURL == "" {
		return events.DynamoDBEventResponse{}, errors.New("cola de conciliacion no configurada")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return events.DynamoDBEventResponse{}, err
	}
	result := (ReconciliationDispatcherApp{Queue: sqs.NewFromConfig(cfg), QueueURL: queueURL, Now: timeNow}).Handle(ctx, event)
	return result, nil
}

var timeNow = time.Now
