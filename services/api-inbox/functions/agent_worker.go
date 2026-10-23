package functions

import (
	"context"
	"encoding/json"
	"log"

	"github.com/aws/aws-lambda-go/events"
)

type AgentMessageProcessor interface {
	Process(context.Context, string, string) error
}

func ProcessAgentQueue(ctx context.Context, processor AgentMessageProcessor, event events.SQSEvent) events.SQSEventResponse {
	result := events.SQSEventResponse{BatchItemFailures: []events.SQSBatchItemFailure{}}
	for _, record := range event.Records {
		var work WorkMessage
		err := json.Unmarshal([]byte(record.Body), &work)
		if err == nil && work.Phone != "" && work.MessageID != "" {
			err = processor.Process(ctx, work.Phone, work.MessageID)
		}
		if err != nil || work.Phone == "" || work.MessageID == "" {
			if err != nil {
				log.Printf("agent processing failed messageId=%s: %v", work.MessageID, err)
			}
			result.BatchItemFailures = append(result.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: record.MessageId})
		}
	}
	return result
}
