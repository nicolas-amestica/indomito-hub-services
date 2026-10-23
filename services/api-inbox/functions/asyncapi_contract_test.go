package functions

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestAsyncAPIContractDefinesSequencedRealtimeStreams(t *testing.T) {
	raw, err := os.ReadFile("../asyncapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		AsyncAPI   string                     `json:"asyncapi"`
		Operations map[string]json.RawMessage `json:"operations"`
		Components struct {
			Messages map[string]json.RawMessage `json:"messages"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document.AsyncAPI != "3.0.0" || document.Operations["receiveInboxEvents"] == nil {
		t.Fatal("AsyncAPI must define the Inbox receive operation")
	}
	for _, name := range []string{"ListEvent", "DetailEvent", "AgentProcessingEvent", "ResyncRequired"} {
		if document.Components.Messages[name] == nil {
			t.Fatalf("AsyncAPI is missing %s", name)
		}
	}
}

func TestAsyncAPIEventNamesMatchOutboxProducers(t *testing.T) {
	contract, err := os.ReadFile("../asyncapi.json")
	if err != nil {
		t.Fatal(err)
	}
	producer, err := os.ReadFile("../domain/conversation_mutation.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []string{"conversation.updated", "conversation.deleted", "agent_processing"} {
		if !strings.Contains(string(contract), `"`+eventType+`"`) {
			t.Fatalf("AsyncAPI is missing producer event %s", eventType)
		}
		if !strings.Contains(string(producer), `"`+eventType+`"`) {
			t.Fatalf("outbox producer is missing contracted event %s", eventType)
		}
	}
}
