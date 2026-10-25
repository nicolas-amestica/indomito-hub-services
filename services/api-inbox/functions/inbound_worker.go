package functions

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"log"

	"github.com/aws/aws-lambda-go/events"
)

// InboundMessageProcessor materializes one verified provider message.
type InboundMessageProcessor interface {
	Process(context.Context, string) error
}

// ProcessInboundQueue handles SQS with partial failures for bounded retries and DLQ isolation.
func ProcessInboundQueue(ctx context.Context, processor InboundMessageProcessor, event events.SQSEvent) events.SQSEventResponse {
	result := events.SQSEventResponse{BatchItemFailures: []events.SQSBatchItemFailure{}}
	for _, record := range event.Records {
		var work WorkMessage
		err := json.Unmarshal([]byte(record.Body), &work)
		if err == nil && work.MessageID != "" {
			err = processor.Process(ctx, work.MessageID)
		}
		if err != nil || work.MessageID == "" {
			fingerprint := sha256.Sum256([]byte(work.MessageID))
			log.Printf("inbound processing failed messageFingerprint=%x: %v", fingerprint[:6], err)
			result.BatchItemFailures = append(result.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: record.MessageId})
		}
	}
	return result
}
