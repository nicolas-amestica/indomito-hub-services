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
	want := AgentCollectedData{Destination: "bariloche", Region: "v", MeetingName: "Ana", MeetingMail: "ana@example.com", MeetingUpdatedAt: now, MeetingContactConfirmedAt: now}
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

func TestAgentRejectsUnverifiedMeetingConfirmationFromModel(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "generativelanguage.googleapis.com" {
			t.Fatalf("unexpected host %q", request.URL.Host)
		}
		var payload struct {
			Input []json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		for _, raw := range payload.Input {
			if strings.Contains(string(raw), "historical-link") {
				t.Fatal("historical agent Meet link was exposed to Gemini")
			}
		}
		return jsonResponse(http.StatusOK, `{"status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"He registrado tu reunión. El enlace es https://meet.google.com/old-link"}]}]}`), nil
	})}
	service := &AgentService{gemini: GeminiConfig{APIKey: "api-key", Model: "model"}, http: client, tools: &AgentToolbox{assets: AgentAssetConfig{}}, now: time.Now}
	content := "Agenda una reunión"
	oldAgentReply := "Tu reunión anterior usó https://meet.google.com/historical-link"
	reply, _, err := service.generate(context.Background(), "56912345678", "wamid-1", []Message{{Type: MessageTypeAIAgent, Content: &oldAgentReply}, {Type: MessageTypeCustomer, Content: &content}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reply, "old-link") || !strings.Contains(reply, "No pude confirmar") {
		t.Fatalf("unverified meeting claim was not blocked: %q", reply)
	}
}

func TestCurrentCustomerTurnKeepsConsecutivePartialContactMessages(t *testing.T) {
	t.Parallel()
	oldAgentReply := "Indícame nombre y correo"
	name := "Nicolás"
	email := "niamesvi3@gmail.com"
	messages := []Message{
		{Type: MessageTypeAIAgent, Content: &oldAgentReply},
		{Type: MessageTypeCustomer, Content: &name},
		{Type: MessageTypeCustomer, Content: &email},
	}
	if got := currentCustomerTurn(messages); got != "Nicolás\nniamesvi3@gmail.com" {
		t.Fatalf("current customer turn = %q", got)
	}
	state := &agentExecutionState{}
	now := time.Date(2026, time.October, 28, 21, 20, 0, 0, time.UTC)
	state.mergeMeeting(map[string]any{"nombreCliente": name, "emailCliente": email}, currentCustomerTurn(messages), now)
	if !state.hasConfirmedMeetingContact(map[string]any{"nombreCliente": name, "emailCliente": email}, now) {
		t.Fatalf("separate contact messages were not preserved: %#v", state.collected)
	}
}

func TestRapidCustomerMessagesOnlyAllowLatestWorker(t *testing.T) {
	t.Parallel()
	firstID, secondID, thirdID := "wamid-1", "wamid-2", "wamid-3"
	name, email, slot := "Nicolás", "niamesvi3@gmail.com", "Mañana a las 16:00"
	messages := []Message{
		{Type: MessageTypeCustomer, Content: &name, WhatsAppMessageID: &firstID},
		{Type: MessageTypeCustomer, Content: &email, WhatsAppMessageID: &secondID},
		{Type: MessageTypeCustomer, Content: &slot, WhatsAppMessageID: &thirdID},
	}
	if isLatestCustomerMessage(messages, firstID) || isLatestCustomerMessage(messages, secondID) {
		t.Fatal("an obsolete rapid-message worker was allowed to execute")
	}
	if !isLatestCustomerMessage(messages, thirdID) {
		t.Fatal("the latest rapid-message worker was rejected")
	}
}

func TestGuardedAgentReplyAllowsOnlyVerifiedMeetLinks(t *testing.T) {
	t.Parallel()
	state := &agentExecutionState{verifiedMeetLinks: map[string]struct{}{"https://meet.google.com/verified-link": {}}}
	want := "Tu reunión actual usa https://meet.google.com/verified-link."
	if got := guardedAgentReply(want, state); got != want {
		t.Fatalf("verified link was blocked: %q", got)
	}
	if got := guardedAgentReply("Usa https://meet.google.com/other-link.", state); !strings.Contains(got, "No pude confirmar") {
		t.Fatalf("unverified link was accepted: %q", got)
	}
}

func TestConfirmedMeetingReplyUsesOnlyToolResult(t *testing.T) {
	t.Parallel()
	reply := confirmedMeetingReply(
		map[string]any{"fecha": "2026-10-29", "hora": "10:00", "meetLink": "https://meet.google.com/new-link"},
		map[string]any{"nombreCliente": "Nicolás", "emailCliente": "niamesvi3@gmail.com"},
	)
	for _, required := range []string{"2026-10-29", "10:00", "Nicolás", "niamesvi3@gmail.com", "new-link"} {
		if !strings.Contains(reply, required) {
			t.Fatalf("confirmation omitted %q: %q", required, reply)
		}
	}
}

func TestConfirmedBudgetDeliveryReplyUsesExactChannelSafeWording(t *testing.T) {
	t.Parallel()
	result := map[string]any{
		"encontrados":         true,
		"certificadoSernatur": map[string]any{"enviado": true},
	}
	args := map[string]any{"destino": "bariloche"}
	want := "Te adjunté las cotizaciones y programas disponibles para Bariloche, junto con nuestro certificado Sernatur."
	if got := confirmedBudgetDeliveryReply(result, args); got != want {
		t.Fatalf("budget delivery confirmation = %q, want %q", got, want)
	}
}

func TestConfirmedBudgetDeliveryReplyDoesNotClaimMissingCertificate(t *testing.T) {
	t.Parallel()
	result := map[string]any{
		"encontrados":         true,
		"certificadoSernatur": map[string]any{"enviado": false},
	}
	args := map[string]any{"destino": "pucon"}
	want := "Te adjunté las cotizaciones y programas disponibles para Pucón."
	if got := confirmedBudgetDeliveryReply(result, args); got != want {
		t.Fatalf("budget delivery confirmation = %q, want %q", got, want)
	}
}

func TestFinalizeBudgetReplyPreservesManagedDetailsAndRemovesFalseEmailClaim(t *testing.T) {
	t.Parallel()
	state := &agentExecutionState{budgetConfirmations: []string{
		"Te adjunté las cotizaciones y programas disponibles para Bariloche, junto con nuestro certificado Sernatur.",
	}}
	modelReply := "Hemos enviado a tu correo las cotizaciones y programas disponibles para Bariloche, junto con nuestro certificado Sernatur. La diferencia principal entre los programas está en la cantidad y combinación de actividades. Además, cada pasajero cuenta con tres talonarios de rifa y existe un 10% de descuento por hermano."
	got := finalizeAgentReply(modelReply, state)
	wantPrefix := "Te adjunté las cotizaciones y programas disponibles para Bariloche, junto con nuestro certificado Sernatur."
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("budget reply omitted exact confirmation: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "correo") || strings.Contains(strings.ToLower(got), "email") {
		t.Fatalf("budget reply retained false delivery channel: %q", got)
	}
	for _, expected := range []string{"diferencia principal", "tres talonarios", "10% de descuento"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("budget reply lost managed detail %q: %q", expected, got)
		}
	}
}

func TestConfirmedMeetingUpdateReplyUsesOnlyToolResult(t *testing.T) {
	t.Parallel()
	reply := confirmedMeetingUpdateReply(map[string]any{"fecha": "2026-10-29", "hora": "19:00", "meetLink": "https://meet.google.com/new-link"})
	for _, required := range []string{"reagendada", "2026-10-29", "19:00", "new-link"} {
		if !strings.Contains(reply, required) {
			t.Fatalf("update confirmation omitted %q: %q", required, reply)
		}
	}
}

func TestExistingMeetingReplyForbidsSecondConcurrentMeeting(t *testing.T) {
	t.Parallel()
	reply := existingMeetingReply()
	for _, required := range []string{"reunión futura", "reagendar", "cancelarla", "no crear una segunda"} {
		if !strings.Contains(reply, required) {
			t.Fatalf("existing meeting response omitted %q: %q", required, reply)
		}
	}
}

func TestAgentReturnsDeterministicResponseWhenMeetingAlreadyExists(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "generativelanguage.googleapis.com":
			return jsonResponse(http.StatusOK, `{"status":"requires_action","steps":[{"type":"function_call","id":"call-1","name":"crear_reunion","arguments":{"nombreCliente":"Nicolas","emailCliente":"niamesvi3@gmail.com","fechaSugerida":"2026-10-29","horaSugerida":"20:30"}}]}`), nil
		case "oauth2.googleapis.com":
			return jsonResponse(http.StatusOK, `{"access_token":"token"}`), nil
		case "www.googleapis.com":
			return jsonResponse(http.StatusOK, `{"items":[{"id":"existing","status":"confirmed","start":{"dateTime":"2026-10-29T14:30:00-03:00"},"end":{"dateTime":"2026-10-29T16:00:00-03:00"}}]}`), nil
		default:
			t.Fatalf("unexpected host %q", request.URL.Host)
			return nil, nil
		}
	})}
	now := time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)
	toolbox := &AgentToolbox{
		meeting: AgentMeetingConfig{CalendarID: "calendar", Timezone: "America/Santiago", SlotDurationMinutes: 90},
		oauth:   GoogleOAuthConfig{ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh"},
		http:    client,
		now:     func() time.Time { return now },
	}
	service := &AgentService{gemini: GeminiConfig{APIKey: "api-key", Model: "model"}, http: client, tools: toolbox, now: func() time.Time { return now }}
	content := "Confirmo, soy Nicolas y mi correo es niamesvi3@gmail.com"
	reply, _, err := service.generate(context.Background(), "56959139687", "wamid-1", []Message{{Type: MessageTypeCustomer, Content: &content}})
	if err != nil {
		t.Fatal(err)
	}
	if reply != existingMeetingReply() {
		t.Fatalf("existing meeting response = %q", reply)
	}
}

func TestGuardedAgentReplyBlocksUnaccentedHallucinationAndHistoryMarker(t *testing.T) {
	t.Parallel()
	state := &agentExecutionState{}
	reply := "Tu reunion quedo agendada. El enlace es [enlace Meet omitido del historial]."
	if got := guardedAgentReply(reply, state); !strings.Contains(got, "No pude confirmar") {
		t.Fatalf("unaccented hallucination was accepted: %q", got)
	}
}

func TestDeterministicMeetingFailureReplies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "invalid email", got: invalidMeetingContactReply(), want: "correo indicado no es válido"},
		{name: "occupied slot", got: unavailableMeetingSlotReply(map[string]any{"horariosDisponibles": []string{"10:00", "11:30"}}), want: "10:00, 11:30"},
		{name: "missing update", got: missingMeetingUpdateReply(), want: "No encontré una reunión futura para reagendar"},
		{name: "missing cancellation", got: missingMeetingCancellationReply(), want: "No encontré una reunión futura para cancelar"},
		{name: "provider failure", got: meetingProviderFailureReply(), want: "No pude confirmar la operación"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !strings.Contains(test.got, test.want) {
				t.Fatalf("reply %q does not contain %q", test.got, test.want)
			}
		})
	}
}

func TestMissingQuoteContextRepliesAreDeterministic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		result map[string]any
		want   string
	}{
		{name: "origin and destination", result: map[string]any{"origenRequerido": true, "destinoAmbiguo": true}, want: "región o comuna sale el grupo y qué destino"},
		{name: "origin", result: map[string]any{"origenRequerido": true}, want: "región o comuna sale el grupo"},
		{name: "destination", result: map[string]any{"destinoAmbiguo": true}, want: "qué destino quieres revisar primero"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := missingQuoteContextReply(test.result); !strings.Contains(got, test.want) {
				t.Fatalf("reply %q does not contain %q", got, test.want)
			}
		})
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
