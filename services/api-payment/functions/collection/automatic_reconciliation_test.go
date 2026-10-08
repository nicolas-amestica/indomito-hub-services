package collection

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

func TestAutomaticReconciliationBackoffIsBounded(t *testing.T) {
	want := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for index, expected := range want {
		if got := automaticReconciliationDelay(int64(index)); got != expected {
			t.Fatalf("attempt %d: got %s want %s", index, got, expected)
		}
	}
}

func TestDecodeReconciliationMessageRejectsIncompleteWork(t *testing.T) {
	if _, err := decodeReconciliationMessage(`{"attemptId":"attempt"}`); err == nil {
		t.Fatal("incomplete job accepted")
	}
	message, err := decodeReconciliationMessage(`{"attemptId":"01M4C8EFRFHRK7DJW09DT6J6CX","accountId":"account","jobSk":"job","count":0}`)
	if err != nil || message.AccountID != "account" {
		t.Fatalf("valid job rejected: %+v %v", message, err)
	}
}

type reconciliationQueueFake struct{ input *sqs.SendMessageInput }

func (f *reconciliationQueueFake) SendMessage(_ context.Context, input *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.input = input
	return &sqs.SendMessageOutput{}, nil
}

func TestReconciliationDispatcherUsesDelayedQueueWithoutPolling(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	queue := &reconciliationQueueFake{}
	event := events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{{EventID: "event", Change: events.DynamoDBStreamRecord{NewImage: map[string]events.DynamoDBAttributeValue{
		"pk":            events.NewStringAttribute(reconciliationPendingPK),
		"sk":            events.NewStringAttribute("job"),
		"attemptId":     events.NewStringAttribute("01M4C8EFRFHRK7DJW09DT6J6CX"),
		"accountId":     events.NewStringAttribute("account"),
		"status":        events.NewStringAttribute("PENDING"),
		"nextAttemptAt": events.NewNumberAttribute("1791460860"),
	}}}}}
	result := (ReconciliationDispatcherApp{Queue: queue, QueueURL: "queue", Now: func() time.Time { return now }}).Handle(context.Background(), event)
	if len(result.BatchItemFailures) != 0 || queue.input == nil || queue.input.DelaySeconds != 60 {
		t.Fatalf("unexpected dispatch: %+v %+v", result, queue.input)
	}
}
