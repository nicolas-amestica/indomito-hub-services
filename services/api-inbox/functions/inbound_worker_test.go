package functions

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

type inboundProcessorFake struct{ failures map[string]error }

func (f inboundProcessorFake) Process(_ context.Context, id string) error { return f.failures[id] }

func TestProcessInboundQueueReportsOnlyInvalidAndFailedMessages(t *testing.T) {
	event := events.SQSEvent{Records: []events.SQSMessage{
		{MessageId: "ok", Body: `{"messageId":"wamid.ok"}`},
		{MessageId: "failed", Body: `{"messageId":"wamid.failed"}`},
		{MessageId: "invalid", Body: `{`},
	}}
	result := ProcessInboundQueue(context.Background(), inboundProcessorFake{failures: map[string]error{"wamid.failed": errors.New("retry")}}, event)
	if len(result.BatchItemFailures) != 2 || result.BatchItemFailures[0].ItemIdentifier != "failed" || result.BatchItemFailures[1].ItemIdentifier != "invalid" {
		t.Fatalf("unexpected failures: %#v", result.BatchItemFailures)
	}
}
