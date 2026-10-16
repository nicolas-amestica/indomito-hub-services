package functions

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type workQueue interface {
	SendMessage(context.Context, *sqs.SendMessageInput, ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// WorkMessage is the durable reference consumed by Inbox workers.
type WorkMessage struct {
	PK          string `json:"pk"`
	SK          string `json:"sk"`
	EventID     string `json:"eventId,omitempty"`
	EventType   string `json:"eventType,omitempty"`
	MessageID   string `json:"messageId,omitempty"`
	OperationID string `json:"operationId,omitempty"`
	Phone       string `json:"phone,omitempty"`
}

// WorkDispatcher forwards newly persisted due work to the bounded SQS queues.
type WorkDispatcher struct {
	Queue       workQueue
	InboundURL  string
	WorkflowURL string
	RealtimeURL string
	AgentURL    string
	Now         func() time.Time
}

// Handle reports individual DynamoDB stream failures so Lambda retries only failed records.
func (d WorkDispatcher) Handle(ctx context.Context, event events.DynamoDBEvent) events.DynamoDBEventResponse {
	result := events.DynamoDBEventResponse{BatchItemFailures: []events.DynamoDBBatchItemFailure{}}
	for _, record := range event.Records {
		message, queueURL, ok := d.decode(record.Change.NewImage)
		if !ok {
			continue
		}
		body, err := json.Marshal(message)
		if err == nil {
			delay := d.delay(record.Change.NewImage)
			_, err = d.Queue.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(queueURL), MessageBody: aws.String(string(body)), DelaySeconds: delay})
		}
		if err != nil {
			result.BatchItemFailures = append(result.BatchItemFailures, events.DynamoDBBatchItemFailure{ItemIdentifier: record.EventID})
		}
	}
	return result
}

func (d WorkDispatcher) decode(image map[string]events.DynamoDBAttributeValue) (WorkMessage, string, bool) {
	pk, pkOK := streamText(image, "pk")
	sk, skOK := streamText(image, "sk")
	state, stateOK := streamText(image, "state")
	if !pkOK || !skOK || !stateOK || state != "pending" || d.Queue == nil || d.InboundURL == "" || d.WorkflowURL == "" || d.RealtimeURL == "" {
		return WorkMessage{}, "", false
	}
	queueURL := d.WorkflowURL
	if strings.HasPrefix(pk, "WORK_DUE#INBOUND#") {
		queueURL = d.InboundURL
	} else if strings.HasPrefix(pk, "WORK_DUE#AGENT#") {
		if d.AgentURL == "" {
			return WorkMessage{}, "", false
		}
		queueURL = d.AgentURL
	} else if strings.HasPrefix(pk, "WORK_DUE#OUTBOX#") {
		outboxType, _ := streamText(image, "outboxType")
		if outboxType != "workflow" {
			queueURL = d.RealtimeURL
		}
	} else if !strings.HasPrefix(pk, "WORK_DUE#WORKFLOW#") {
		return WorkMessage{}, "", false
	}
	message := WorkMessage{PK: pk, SK: sk}
	message.EventID, _ = streamText(image, "eventId")
	message.EventType, _ = streamText(image, "eventType")
	message.MessageID, _ = streamText(image, "messageId")
	message.OperationID, _ = streamText(image, "operationId")
	message.Phone, _ = streamText(image, "phone")
	return message, queueURL, true
}

func (d WorkDispatcher) delay(image map[string]events.DynamoDBAttributeValue) int32 {
	next, ok := streamText(image, "nextAttemptAt")
	if !ok || d.Now == nil {
		return 0
	}
	at, err := time.Parse(time.RFC3339Nano, next)
	if err != nil {
		return 0
	}
	seconds := int64(at.Sub(d.Now().UTC()).Seconds())
	if seconds < 0 {
		return 0
	}
	if seconds > 900 {
		return 900
	}
	return int32(seconds)
}

func streamText(image map[string]events.DynamoDBAttributeValue, key string) (string, bool) {
	value, ok := image[key]
	return value.String(), ok && value.DataType() == events.DataTypeString
}
