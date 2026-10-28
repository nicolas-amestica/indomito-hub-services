package functions

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

func TestInboundWorkerLoadKeepsPartialRetryBoundary(t *testing.T) {
	const records = 10_000
	failures := make(map[string]error, records/20)
	event := events.SQSEvent{Records: make([]events.SQSMessage, 0, records)}
	for index := 0; index < records; index++ {
		messageID := fmt.Sprintf("wamid.load.%05d", index)
		queueID := fmt.Sprintf("record-%05d", index)
		event.Records = append(event.Records, events.SQSMessage{MessageId: queueID, Body: fmt.Sprintf(`{"messageId":%q}`, messageID)})
		if index%20 == 0 {
			failures[messageID] = errors.New("controlled retry")
		}
	}

	started := time.Now()
	result := ProcessInboundQueue(context.Background(), inboundProcessorFake{failures: failures}, event)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("10k-record local resilience run exceeded budget: %s", elapsed)
	}
	if len(result.BatchItemFailures) != records/20 {
		t.Fatalf("partial failures = %d, want %d", len(result.BatchItemFailures), records/20)
	}
	for index, failure := range result.BatchItemFailures {
		want := fmt.Sprintf("record-%05d", index*20)
		if failure.ItemIdentifier != want {
			t.Fatalf("failure %d = %q, want %q", index, failure.ItemIdentifier, want)
		}
	}
}
