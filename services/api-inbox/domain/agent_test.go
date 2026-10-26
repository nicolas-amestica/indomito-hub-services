package domain

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

type agentStateDynamoFake struct {
	item map[string]types.AttributeValue
}

func (f *agentStateDynamoFake) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: f.item}, nil
}

func (f *agentStateDynamoFake) PutItem(_ context.Context, input *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.item = input.Item
	return &dynamodb.PutItemOutput{}, nil
}

func (f *agentStateDynamoFake) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

func (f *agentStateDynamoFake) TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func TestAgentCollectedDataRoundTripUsesExpiringConversationItem(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 23, 12, 0, 0, 0, time.UTC)
	db := &agentStateDynamoFake{}
	service := &AgentService{db: db, table: "table", now: func() time.Time { return now }}
	want := AgentCollectedData{Destination: "bariloche", Region: "v", MeetingName: "Ana", MeetingMail: "ana@example.com"}
	if err := service.saveAgentCollectedData(context.Background(), "56912345678", want); err != nil {
		t.Fatal(err)
	}
	got, err := service.agentCollectedData(context.Background(), "56912345678")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("collected data = %#v, want %#v", got, want)
	}
	if ttl, ok := db.item["expiresAt"].(*types.AttributeValueMemberN); !ok || ttl.Value != "1795348800" {
		t.Fatalf("unexpected ttl %#v", db.item["expiresAt"])
	}
}

func TestAgentGenerateUsesStatelessInteractionsToolLoop(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 23, 9, 0, 0, 0, location)
	interactionCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "generativelanguage.googleapis.com":
			interactionCalls++
			if request.URL.Path != "/v1beta/interactions" || request.Header.Get("x-goog-api-key") != "api-key" {
				t.Fatalf("unexpected Gemini request: %s headers=%v", request.URL, request.Header)
			}
			var payload struct {
				Store bool              `json:"store"`
				Input []json.RawMessage `json:"input"`
				Tools []map[string]any  `json:"tools"`
			}
			if decodeErr := json.NewDecoder(request.Body).Decode(&payload); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if payload.Store || len(payload.Tools) != len(agentFunctionDeclarations()) {
				t.Fatalf("unexpected interaction settings: store=%v tools=%d", payload.Store, len(payload.Tools))
			}
			if interactionCalls == 1 {
				return jsonResponse(http.StatusOK, `{"status":"requires_action","steps":[{"type":"function_call","id":"call-1","name":"listar_reuniones","arguments":{"fechaInicio":"2026-10-23","fechaFin":"2026-10-23"}}]}`), nil
			}
			foundResult := false
			for _, raw := range payload.Input {
				var step interactionStep
				if json.Unmarshal(raw, &step) == nil && step.Type == "function_result" {
					foundResult = true
				}
			}
			if !foundResult {
				t.Fatal("second interaction omitted the function result")
			}
			return jsonResponse(http.StatusOK, `{"status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"Tengo disponibilidad hoy a las 10:30. Necesito tu nombre y correo."}]}]}`), nil
		case "oauth2.googleapis.com":
			return jsonResponse(http.StatusOK, `{"access_token":"calendar-token"}`), nil
		case "www.googleapis.com":
			return jsonResponse(http.StatusOK, `{"items":[]}`), nil
		default:
			t.Fatalf("unexpected host %q", request.URL.Host)
			return nil, nil
		}
	})}
	toolbox := &AgentToolbox{
		meeting: AgentMeetingConfig{CalendarID: "calendar", Timezone: "America/Santiago", SlotDurationMinutes: 90, WeekdayStartHour: 10, WeekdayEndHour: 22, SaturdayStartHour: 10, SaturdayEndHour: 14},
		oauth:   GoogleOAuthConfig{ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh"},
		http:    client,
		now:     func() time.Time { return now },
	}
	service := &AgentService{gemini: GeminiConfig{APIKey: "api-key", Model: "gemini-3.1-pro-preview-customtools", Temperature: 0.2, TopP: 0.9}, http: client, tools: toolbox, now: func() time.Time { return now }}
	content := "Puedes agendar una reunión para hoy?"
	reply, derivation, err := service.generate(context.Background(), "56912345678", "wamid-1", []Message{{Type: MessageTypeCustomer, Content: &content}})
	if err != nil {
		t.Fatal(err)
	}
	if derivation != nil || interactionCalls != 2 || !strings.Contains(reply, "10:30") {
		t.Fatalf("reply=%q derivation=%#v calls=%d", reply, derivation, interactionCalls)
	}
}

func TestGeminiStatusErrorIncludesSafeProviderDetail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "provider detail", status: http.StatusNotFound, body: `{"error":{"status":"NOT_FOUND","message":"model unavailable"}}`, want: "gemini status 404 (NOT_FOUND): model unavailable"},
		{name: "invalid body", status: http.StatusServiceUnavailable, body: `not-json`, want: "gemini status 503"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := geminiStatusError(test.status, []byte(test.body)).Error(); got != test.want {
				t.Fatalf("geminiStatusError() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGeminiQuotaExhaustedRecognizesOnlyResourceExhausted429(t *testing.T) {
	t.Parallel()
	quotaError := geminiStatusError(http.StatusTooManyRequests, []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","message":"free tier quota exhausted"}}`))
	if !isGeminiQuotaExhausted(quotaError) {
		t.Fatal("expected RESOURCE_EXHAUSTED 429 to be recognized as quota exhaustion")
	}
	if isGeminiQuotaExhausted(geminiStatusError(http.StatusServiceUnavailable, []byte(`{"error":{"status":"RESOURCE_EXHAUSTED"}}`))) {
		t.Fatal("unexpected quota exhaustion for non-429 status")
	}
	if isGeminiQuotaExhausted(geminiStatusError(http.StatusTooManyRequests, []byte(`{"error":{"status":"RATE_LIMITED","message":"try later"}}`))) {
		t.Fatal("unexpected quota exhaustion for another provider code")
	}
}

func TestExplicitHumanHandoffDetection(t *testing.T) {
	t.Parallel()
	for _, message := range []string{"Quiero hablar con una persona", "Necesito un agente real", "Quiero atención humana", "Puedo hablar con un ejecutivo?"} {
		if !isExplicitHumanHandoff(message) {
			t.Fatalf("expected handoff request for %q", message)
		}
	}
	for _, message := range []string{"Quiero una cotización", "Una persona paga cuánto?", "Gracias"} {
		if isExplicitHumanHandoff(message) {
			t.Fatalf("unexpected handoff request for %q", message)
		}
	}
}
