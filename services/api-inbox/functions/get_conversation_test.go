package functions

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

type fakeConversationDetailer struct {
	mode     string
	phone    string
	before   string
	pageSize int
	since    time.Time
}

func (fake *fakeConversationDetailer) Page(_ context.Context, _, phone, before string, pageSize int) (domain.ConversationDetailResponse, error) {
	fake.mode, fake.phone, fake.before, fake.pageSize = "page", phone, before, pageSize
	return domain.ConversationDetailResponse{Mode: "full", Messages: []domain.Message{}, NonTerminalSends: []domain.NonTerminalSend{}}, nil
}

func (fake *fakeConversationDetailer) Delta(_ context.Context, _, phone string, since time.Time) (domain.ConversationDetailResponse, error) {
	fake.mode, fake.phone, fake.since = "delta", phone, since
	return domain.ConversationDetailResponse{Mode: "delta", Messages: []domain.Message{}, NonTerminalSends: []domain.NonTerminalSend{}}, nil
}

func TestGetConversationFullAndDelta(t *testing.T) {
	tests := []struct {
		name  string
		query map[string]string
		mode  string
	}{
		{name: "full", query: map[string]string{"pageSize": "25"}, mode: "page"},
		{name: "delta", query: map[string]string{"since": "2026-10-14T12:00:00Z"}, mode: "delta"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeConversationDetailer{}
			request := listRequest(test.query)
			request.PathParameters = map[string]string{"telefono": "56912345678"}
			response, err := GetConversation(context.Background(), fake, request)
			if err != nil || response.StatusCode != 200 || fake.mode != test.mode {
				t.Fatalf("response = %#v, fake = %#v, error = %v", response, fake, err)
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
				t.Fatal(err)
			}
			if _, ok := body["data"].(map[string]any); !ok {
				t.Fatalf("body = %#v", body)
			}
		})
	}
}

func TestGetConversationRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		phone string
		query map[string]string
		code  string
	}{
		{name: "phone", phone: "+5691", code: "VALIDATION_PHONE_INVALID"},
		{name: "page size", phone: "56912345678", query: map[string]string{"pageSize": "51"}, code: "VALIDATION_INPUT_INVALID"},
		{name: "exclusive modes", phone: "56912345678", query: map[string]string{"before": "x", "since": "2026-10-14T12:00:00Z"}, code: "VALIDATION_INPUT_INVALID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := listRequest(test.query)
			request.PathParameters = map[string]string{"telefono": test.phone}
			response, _ := GetConversation(context.Background(), &fakeConversationDetailer{}, request)
			assertErrorCode(t, response, 400, test.code)
		})
	}
}
