package domain

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type sendDynamoFake struct {
	item   map[string]types.AttributeValue
	writes []types.TransactWriteItem
}

func (fake *sendDynamoFake) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: fake.item}, nil
}

func (*sendDynamoFake) PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}

func (*sendDynamoFake) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

func (*sendDynamoFake) UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{}, nil
}

func (fake *sendDynamoFake) TransactWriteItems(_ context.Context, input *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	fake.writes = input.TransactItems
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func TestDynamoSendRepositoryCommitPersistsFullContent(t *testing.T) {
	now := time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC)
	content := strings.Repeat("respuesta completa ", 20)
	preview := sanitizeSendText(content)
	record := sendRecord{
		PK: "SEND#client-message", SK: "META", OwnerID: "agent", Content: content, Actor: MessageTypeAIAgent,
		SendState: SendState{ClientMessageID: "client-message", Phone: "56912345678", State: SendProviderPending, CreatedAt: now, UpdatedAt: now, Preview: &preview},
	}
	item, err := attributevalue.MarshalMap(record)
	if err != nil {
		t.Fatal(err)
	}
	fake := &sendDynamoFake{item: item}
	repository, err := NewDynamoSendRepository(fake, "table")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = repository.Commit(context.Background(), "agent", "client-message", "wamid.sent", now); err != nil {
		t.Fatal(err)
	}
	if len(fake.writes) == 0 || fake.writes[0].Put == nil {
		t.Fatal("commit did not persist the message")
	}
	var message Message
	if err = attributevalue.UnmarshalMap(fake.writes[0].Put.Item, &message); err != nil {
		t.Fatal(err)
	}
	if message.Content == nil || *message.Content != content || len([]rune(*message.Content)) <= 200 {
		t.Fatalf("persisted content length = %d, want %d", len([]rune(valueOrEmpty(message.Content))), len([]rune(content)))
	}
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
