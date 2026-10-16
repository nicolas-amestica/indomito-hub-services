package domain

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeDetailReader struct {
	calls        []string
	conversation map[string]types.AttributeValue
	messages     []map[string]types.AttributeValue
	sends        []map[string]types.AttributeValue
	messageStart map[string]types.AttributeValue
}

func (fake *fakeDetailReader) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	pk := input.Key["pk"].(*types.AttributeValueMemberS).Value
	fake.calls = append(fake.calls, "get:"+pk)
	if input.ConsistentRead == nil || !*input.ConsistentRead {
		return nil, fmt.Errorf("inconsistent GetItem")
	}
	if pk[:4] == "SEQ#" {
		item, _ := attributevalue.MarshalMap(listSequence{Sequence: 77})
		return &dynamodb.GetItemOutput{Item: item}, nil
	}
	return &dynamodb.GetItemOutput{Item: fake.conversation}, nil
}

func (fake *fakeDetailReader) Query(_ context.Context, input *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	pk := input.ExpressionAttributeValues[":pk"].(*types.AttributeValueMemberS).Value
	fake.calls = append(fake.calls, "query:"+pk)
	if input.ConsistentRead == nil || !*input.ConsistentRead {
		return nil, fmt.Errorf("inconsistent Query")
	}
	if pk[:5] == "CONV#" {
		fake.messageStart = input.ExclusiveStartKey
		return &dynamodb.QueryOutput{Items: fake.messages}, nil
	}
	return &dynamodb.QueryOutput{Items: fake.sends}, nil
}

func TestReadConversationDetailCapturesSequenceBeforeData(t *testing.T) {
	conversation, _ := attributevalue.MarshalMap(ConversationSummary{ID: "conv", Phone: "56912345678", Alias: "Ana", Channel: "whatsapp", Status: ConversationStatusBot})
	messages := make([]map[string]types.AttributeValue, 0, 4)
	for index := 4; index >= 1; index-- {
		item, _ := attributevalue.MarshalMap(Message{ID: fmt.Sprint(index), MessageSK: fmt.Sprintf("MSG#%02d", index), Phone: "56912345678", Type: MessageTypeCustomer, CreatedAt: time.Date(2026, 10, 14, 12, index, 0, 0, time.UTC), Attachments: []MessageAttachment{}})
		messages = append(messages, item)
	}
	send, _ := attributevalue.MarshalMap(NonTerminalSend{ClientMessageID: "client-1", Phone: "56912345678", State: "provider_pending", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	fake := &fakeDetailReader{conversation: conversation, messages: messages, sends: []map[string]types.AttributeValue{send}}

	detail, err := ReadConversationDetail(context.Background(), fake, "table", "owner", "56912345678", "", 3)
	if err != nil {
		t.Fatalf("ReadConversationDetail() error = %v", err)
	}
	wantCalls := []string{"get:SEQ#DETAIL#56912345678", "get:CONV#56912345678", "query:CONV#56912345678", "query:SEND_VIEW#owner#56912345678"}
	if fmt.Sprint(fake.calls) != fmt.Sprint(wantCalls) {
		t.Fatalf("calls = %v, want %v", fake.calls, wantCalls)
	}
	if detail.Sequence != 77 || !detail.HasMore || detail.OldestMessageSK != "MSG#02" || len(detail.Messages) != 3 || detail.Messages[0].ID != "2" || detail.Messages[2].ID != "4" || len(detail.NonTerminalSends) != 1 {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestReadConversationDetailReturnsNormativeFallback(t *testing.T) {
	fake := &fakeDetailReader{}
	detail, err := ReadConversationDetail(context.Background(), fake, "table", "owner", "56912345678", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Conversation.Phone != "56912345678" || detail.Conversation.Alias != "56912345678" || detail.Conversation.Channel != "whatsapp" || detail.Conversation.Status != ConversationStatusBot {
		t.Fatalf("fallback = %#v", detail.Conversation)
	}
}

func TestReadConversationDetailUsesExclusiveStartKey(t *testing.T) {
	fake := &fakeDetailReader{}
	_, err := ReadConversationDetail(context.Background(), fake, "table", "owner", "56912345678", "MSG#10", 50)
	if err != nil {
		t.Fatal(err)
	}
	if fake.messageStart == nil || fake.messageStart["sk"].(*types.AttributeValueMemberS).Value != "MSG#10" {
		t.Fatalf("exclusive start key = %#v", fake.messageStart)
	}
}
