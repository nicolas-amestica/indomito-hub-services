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
	item    map[string]types.AttributeValue
	writes  []types.TransactWriteItem
	updates int
	puts    int
}

func (fake *sendDynamoFake) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: fake.item}, nil
}

func (fake *sendDynamoFake) PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	fake.puts++
	return &dynamodb.PutItemOutput{}, nil
}

func (*sendDynamoFake) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

func (fake *sendDynamoFake) UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	fake.updates++
	return &dynamodb.UpdateItemOutput{}, nil
}

func TestDynamoSendRepositoryFailRemovesOnlyTerminalViews(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name             string
		state            SendStateName
		wantTransactions int
		wantUpdates      int
		wantPuts         int
	}{
		{name: "terminal failure", state: SendFailed, wantTransactions: 2},
		{name: "retryable failure", state: SendFailedRetryable, wantUpdates: 1, wantPuts: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			preview := "test"
			record := sendRecord{PK: "SEND#client-message", SK: "META", OwnerID: "owner", SendState: SendState{ClientMessageID: "client-message", Phone: "56912345678", State: SendProviderPending, CreatedAt: now, UpdatedAt: now, Preview: &preview}}
			item, err := attributevalue.MarshalMap(record)
			if err != nil {
				t.Fatal(err)
			}
			fake := &sendDynamoFake{item: item}
			repository, err := NewDynamoSendRepository(fake, "table")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repository.Fail(context.Background(), "owner", "client-message", test.state, "failure", now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if len(fake.writes) != test.wantTransactions || fake.updates != test.wantUpdates || fake.puts != test.wantPuts {
				t.Fatalf("writes=%d updates=%d puts=%d", len(fake.writes), fake.updates, fake.puts)
			}
			if test.state == SendFailed {
				if fake.writes[0].Update == nil || fake.writes[1].Delete == nil {
					t.Fatalf("terminal transaction = %#v", fake.writes)
				}
				viewPK := fake.writes[1].Delete.Key["pk"].(*types.AttributeValueMemberS).Value
				if viewPK != "SEND_VIEW#owner#56912345678" {
					t.Fatalf("deleted view partition = %q", viewPK)
				}
			}
		})
	}
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
