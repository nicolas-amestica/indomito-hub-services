package domain

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestConversationDetailServiceFullAndBefore(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	codec, _ := NewListCursorCodec([]byte(strings.Repeat("s", 32)), func() time.Time { return now })
	conversation, _ := attributevalue.MarshalMap(ConversationSummary{ID: "conv", Phone: "56912345678", Alias: "Ana", Channel: "whatsapp", Status: ConversationStatusBot})
	messages := make([]map[string]types.AttributeValue, 0, 3)
	for _, sk := range []string{"MSG#03", "MSG#02", "MSG#01"} {
		item, _ := attributevalue.MarshalMap(Message{ID: sk, MessageSK: sk, Phone: "56912345678", Type: MessageTypeCustomer, CreatedAt: now, Attachments: []MessageAttachment{}})
		messages = append(messages, item)
	}
	fake := &fakeDetailReader{conversation: conversation, messages: messages}
	service, _ := NewConversationDetailService(fake, codec, "table")

	full, err := service.Page(context.Background(), "owner", "56912345678", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if full.Mode != "full" || full.Conversation == nil || full.NextCursor == nil || len(full.Messages) != 2 {
		t.Fatalf("full = %#v", full)
	}
	before, err := service.Page(context.Background(), "owner", "56912345678", *full.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode != "before" || before.Conversation != nil || before.RequestedBefore == nil || len(before.NonTerminalSends) != 0 {
		t.Fatalf("before = %#v", before)
	}
}

func TestDetailCursorRejectsDifferentPhoneOrOwner(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	codec, _ := NewListCursorCodec([]byte(strings.Repeat("s", 32)), func() time.Time { return now })
	token, _ := codec.encodeDetail("owner", "56912345678", "MSG#01")
	for _, test := range []struct{ owner, phone string }{{"other", "56912345678"}, {"owner", "56999999999"}} {
		if _, err := codec.decodeDetail(token, test.owner, test.phone); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("decode error = %v", err)
		}
	}
}
