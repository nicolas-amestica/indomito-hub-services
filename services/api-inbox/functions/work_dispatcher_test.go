package functions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type queueFake struct {
	inputs []*sqs.SendMessageInput
	err    error
}

func (f *queueFake) SendMessage(_ context.Context, input *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.inputs = append(f.inputs, input)
	return &sqs.SendMessageOutput{}, f.err
}

func TestWorkDispatcherRoutesDurableWork(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct{ name, pk, queue string }{
		{name: "inbound", pk: "WORK_DUE#INBOUND#01", queue: "inbound"},
		{name: "bulk", pk: "WORK_DUE#WORKFLOW#02", queue: "workflow"},
		{name: "realtime", pk: "WORK_DUE#OUTBOX#03", queue: "realtime"},
		{name: "workflow outbox", pk: "WORK_DUE#OUTBOX#04", queue: "workflow"},
		{name: "agent", pk: "WORK_DUE#AGENT#05", queue: "agent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queue := &queueFake{}
			dispatcher := WorkDispatcher{Queue: queue, InboundURL: "inbound", WorkflowURL: "workflow", RealtimeURL: "realtime", AgentURL: "agent", Now: func() time.Time { return now }}
			image := map[string]events.DynamoDBAttributeValue{
				"pk": events.NewStringAttribute(test.pk), "sk": events.NewStringAttribute("due#event"),
				"state": events.NewStringAttribute("pending"), "eventId": events.NewStringAttribute("event"),
				"nextAttemptAt": events.NewStringAttribute(now.Add(16 * time.Minute).Format(time.RFC3339Nano)),
			}
			if test.name == "workflow outbox" {
				image["outboxType"] = events.NewStringAttribute("workflow")
			}
			result := dispatcher.Handle(context.Background(), events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{{EventID: "record", Change: events.DynamoDBStreamRecord{NewImage: image}}}})
			if len(result.BatchItemFailures) != 0 || len(queue.inputs) != 1 || *queue.inputs[0].QueueUrl != test.queue || queue.inputs[0].DelaySeconds != 900 {
				t.Fatalf("unexpected dispatch result: %#v %#v", result, queue.inputs)
			}
			var message WorkMessage
			if err := json.Unmarshal([]byte(*queue.inputs[0].MessageBody), &message); err != nil || message.PK != test.pk {
				t.Fatalf("unexpected work message: %#v, %v", message, err)
			}
		})
	}
}

func TestWorkDispatcherReportsOnlySendFailures(t *testing.T) {
	queue := &queueFake{err: errors.New("unavailable")}
	dispatcher := WorkDispatcher{Queue: queue, InboundURL: "inbound", WorkflowURL: "workflow", RealtimeURL: "realtime", Now: time.Now}
	pending := map[string]events.DynamoDBAttributeValue{"pk": events.NewStringAttribute("WORK_DUE#INBOUND#01"), "sk": events.NewStringAttribute("due"), "state": events.NewStringAttribute("pending")}
	ignored := map[string]events.DynamoDBAttributeValue{"pk": events.NewStringAttribute("OTHER"), "sk": events.NewStringAttribute("due"), "state": events.NewStringAttribute("pending")}
	result := dispatcher.Handle(context.Background(), events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{{EventID: "failed", Change: events.DynamoDBStreamRecord{NewImage: pending}}, {EventID: "ignored", Change: events.DynamoDBStreamRecord{NewImage: ignored}}}})
	if len(result.BatchItemFailures) != 1 || result.BatchItemFailures[0].ItemIdentifier != "failed" {
		t.Fatalf("unexpected failures: %#v", result.BatchItemFailures)
	}
}
