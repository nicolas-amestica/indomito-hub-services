package functions

import (
	"context"
	"testing"

	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

type conversationMutatorFake struct{ marked, activated, deleted string }

func (f *conversationMutatorFake) MarkUnread(_ context.Context, phone string) (domain.ConversationSummary, error) {
	f.marked = phone
	return domain.ConversationSummary{ID: phone, Phone: phone, Alias: "Cliente", Channel: "whatsapp", Status: domain.ConversationStatusBot, UnreadCount: 1}, nil
}
func (f *conversationMutatorFake) ActivateBot(_ context.Context, phone string) (domain.ConversationSummary, error) {
	f.activated = phone
	return domain.ConversationSummary{ID: phone, Phone: phone, Alias: "Cliente", Channel: "whatsapp", Status: domain.ConversationStatusBot}, nil
}
func (f *conversationMutatorFake) DeleteConversation(_ context.Context, phone string) (domain.DeleteConversationResult, error) {
	f.deleted = phone
	return domain.DeleteConversationResult{Deleted: true}, nil
}

func TestReactivateConversationAgentRequiresExactBotStatus(t *testing.T) {
	tests := []struct {
		name, body string
		want       int
	}{{name: "valid", body: `{"status":"bot"}`, want: 200}, {name: "derived forbidden", body: `{"status":"derived"}`, want: 400}, {name: "extra field", body: `{"status":"bot","other":1}`, want: 400}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := listRequest(nil)
			request.PathParameters = map[string]string{"telefono": "56912345678"}
			request.Body = test.body
			fake := &conversationMutatorFake{}
			response, err := ReactivateConversationAgent(context.Background(), fake, request)
			if err != nil || response.StatusCode != test.want {
				t.Fatalf("status=%d err=%v body=%s", response.StatusCode, err, response.Body)
			}
			if test.want == 200 && fake.activated != "56912345678" {
				t.Fatalf("activated=%q", fake.activated)
			}
		})
	}
}

func TestMarkConversationUnreadRequiresExactTrue(t *testing.T) {
	tests := []struct {
		name, body string
		want       int
	}{{name: "valid", body: `{"unread":true}`, want: 200}, {name: "false", body: `{"unread":false}`, want: 400}, {name: "extra field", body: `{"unread":true,"other":1}`, want: 400}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := listRequest(nil)
			request.PathParameters = map[string]string{"telefono": "56912345678"}
			request.Body = test.body
			fake := &conversationMutatorFake{}
			response, err := MarkConversationUnread(context.Background(), fake, request)
			if err != nil || response.StatusCode != test.want {
				t.Fatalf("status=%d err=%v body=%s", response.StatusCode, err, response.Body)
			}
			if test.want == 200 && fake.marked != "56912345678" {
				t.Fatalf("marked=%q", fake.marked)
			}
		})
	}
}

func TestDeleteConversationRejectsBodyAndIsIdempotentResponse(t *testing.T) {
	tests := []struct {
		name, body string
		want       int
	}{{name: "empty", want: 200}, {name: "body forbidden", body: `{}`, want: 400}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := listRequest(nil)
			request.PathParameters = map[string]string{"telefono": "56912345678"}
			request.Body = test.body
			fake := &conversationMutatorFake{}
			response, err := DeleteConversation(context.Background(), fake, request)
			if err != nil || response.StatusCode != test.want {
				t.Fatalf("status=%d err=%v body=%s", response.StatusCode, err, response.Body)
			}
			if test.want == 200 && fake.deleted != "56912345678" {
				t.Fatalf("deleted=%q", fake.deleted)
			}
		})
	}
}
