package functions

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

var cached struct {
	sync.Mutex
	until time.Time
	deps  dependencies
}

func loadDependencies(ctx context.Context) (dependencies, error) {
	if os.Getenv("APP_STAGE") != "dev" || os.Getenv("PAYMENTS_TABLE_NAME") == "" || os.Getenv("RECEIPTS_BUCKET_NAME") == "" {
		return dependencies{}, errors.New("DEV_CONFIGURATION_REQUIRED")
	}
	cached.Lock()
	defer cached.Unlock()
	if time.Now().Before(cached.until) {
		return cached.deps, nil
	}
	awsConfig, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return dependencies{}, err
	}
	smtpSettings, err := loadSMTP(ctx, ssm.NewFromConfig(awsConfig))
	if err != nil {
		return dependencies{}, err
	}
	db := dynamodb.NewFromConfig(awsConfig)
	table := os.Getenv("PAYMENTS_TABLE_NAME")
	root := store{client: db, table: table, now: time.Now}
	deps := dependencies{store: root, deliveryStore: func(receiptID string) receiptStore {
		return store{client: db, table: table, parentReceiptID: receiptID, now: time.Now}
	}, documents: documents{client: s3.NewFromConfig(awsConfig), bucket: os.Getenv("RECEIPTS_BUCKET_NAME")}, mail: mailer{config: smtpSettings}}
	cached.deps, cached.until = deps, time.Now().Add(5*time.Minute)
	return deps, nil
}

// Handler procesa únicamente trabajos JOB pendientes del Stream de pagos.
func Handler(ctx context.Context, event events.DynamoDBEvent) (events.DynamoDBEventResponse, error) {
	deps, err := loadDependencies(ctx)
	if err != nil {
		return events.DynamoDBEventResponse{}, err
	}
	return processStream(ctx, event, deps), nil
}
