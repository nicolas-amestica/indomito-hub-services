package functions

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

type agentProcessorCall struct {
	phone     string
	messageID string
}

type agentProcessorFake struct {
	calls []agentProcessorCall
	err   error
}

func (f *agentProcessorFake) Process(_ context.Context, phone, messageID string) error {
	f.calls = append(f.calls, agentProcessorCall{phone: phone, messageID: messageID})
	return f.err
}

func TestProcessAgentQueuePreservesAgentBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		body         string
		processorErr error
		wantCalls    []agentProcessorCall
		wantFailure  bool
	}{
		{
			name:      "valid work keeps phone and message id",
			body:      `{"phone":"56912345678","messageId":"wamid.agent-1"}`,
			wantCalls: []agentProcessorCall{{phone: "56912345678", messageID: "wamid.agent-1"}},
		},
		{
			name:         "processor error remains retryable",
			body:         `{"phone":"56912345678","messageId":"wamid.agent-2"}`,
			processorErr: errors.New("temporary agent failure"),
			wantCalls:    []agentProcessorCall{{phone: "56912345678", messageID: "wamid.agent-2"}},
			wantFailure:  true,
		},
		{name: "malformed work never reaches agent", body: `{`, wantFailure: true},
		{name: "missing phone never reaches agent", body: `{"messageId":"wamid.agent-3"}`, wantFailure: true},
		{name: "missing message id never reaches agent", body: `{"phone":"56912345678"}`, wantFailure: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			processor := &agentProcessorFake{err: test.processorErr}
			response := ProcessAgentQueue(context.Background(), processor, events.SQSEvent{Records: []events.SQSMessage{{
				MessageId: "sqs-message",
				Body:      test.body,
			}}})

			if len(processor.calls) != len(test.wantCalls) {
				t.Fatalf("processor calls = %#v, want %#v", processor.calls, test.wantCalls)
			}
			for index := range test.wantCalls {
				if processor.calls[index] != test.wantCalls[index] {
					t.Fatalf("processor call %d = %#v, want %#v", index, processor.calls[index], test.wantCalls[index])
				}
			}
			if gotFailure := len(response.BatchItemFailures) == 1; gotFailure != test.wantFailure {
				t.Fatalf("failure reported = %v, want %v: %#v", gotFailure, test.wantFailure, response.BatchItemFailures)
			}
			if test.wantFailure && response.BatchItemFailures[0].ItemIdentifier != "sqs-message" {
				t.Fatalf("failure item = %q, want sqs-message", response.BatchItemFailures[0].ItemIdentifier)
			}
		})
	}
}

func TestProcessAgentQueueReportsOnlyFailedRecords(t *testing.T) {
	t.Parallel()
	processor := &agentProcessorFake{}
	response := ProcessAgentQueue(context.Background(), processor, events.SQSEvent{Records: []events.SQSMessage{
		{MessageId: "valid", Body: `{"phone":"56912345678","messageId":"wamid.valid"}`},
		{MessageId: "invalid", Body: `{"phone":"56912345678"}`},
	}})

	if len(processor.calls) != 1 || processor.calls[0].messageID != "wamid.valid" {
		t.Fatalf("valid record processing changed: %#v", processor.calls)
	}
	if len(response.BatchItemFailures) != 1 || response.BatchItemFailures[0].ItemIdentifier != "invalid" {
		t.Fatalf("partial batch failures = %#v, want only invalid", response.BatchItemFailures)
	}
}
