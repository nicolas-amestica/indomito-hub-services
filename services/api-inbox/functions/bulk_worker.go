package functions

import (
	"context"
	"encoding/json"

	"github.com/aws/aws-lambda-go/events"
)

// BulkProcessor applies one durable bulk operation.
type BulkProcessor interface {
	Process(context.Context, string) error
}

// ProcessBulkQueue reports individual failures for SQS retry and DLQ isolation.
func ProcessBulkQueue(ctx context.Context, processor BulkProcessor, event events.SQSEvent) events.SQSEventResponse {
	result := events.SQSEventResponse{BatchItemFailures: []events.SQSBatchItemFailure{}}
	for _, record := range event.Records {
		var work WorkMessage
		err := json.Unmarshal([]byte(record.Body), &work)
		if err == nil && work.OperationID != "" {
			err = processor.Process(ctx, work.OperationID)
		}
		if err != nil || work.OperationID == "" {
			result.BatchItemFailures = append(result.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: record.MessageId})
		}
	}
	return result
}
