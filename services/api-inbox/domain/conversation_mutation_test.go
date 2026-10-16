package domain

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type mutationDynamoFake struct {
	items  map[string]map[string]types.AttributeValue
	writes [][]types.TransactWriteItem
}

func (f *mutationDynamoFake) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	pk := input.Key["pk"].(*types.AttributeValueMemberS).Value
	return &dynamodb.GetItemOutput{Item: f.items[pk]}, nil
}
func (f *mutationDynamoFake) TransactWriteItems(_ context.Context, input *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	f.writes = append(f.writes, input.TransactItems)
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func TestConversationMutationServiceMarkUnreadWritesAtomicProjections(t *testing.T) {
	fixed := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	summary := ConversationSummary{ID: "56912345678", Phone: "56912345678", Alias: "Cliente", Channel: "whatsapp", Status: ConversationStatusBot, Generation: 4}
	conversation, _ := attributevalue.MarshalMap(summary)
	listSequenceItem, _ := attributevalue.MarshalMap(listSequence{Sequence: 12})
	detailSequence, _ := attributevalue.MarshalMap(listSequence{Sequence: 7})
	fake := &mutationDynamoFake{items: map[string]map[string]types.AttributeValue{"CONV#56912345678": conversation, listSequencePK: listSequenceItem, "SEQ#DETAIL#56912345678": detailSequence}}
	service, err := NewConversationMutationService(fake, "table", func() time.Time { return fixed })
	if err != nil {
		t.Fatal(err)
	}
	service.newID = func(time.Time) (string, error) { return "01K7J8QZD00000000000000000", nil }
	got, err := service.MarkUnread(context.Background(), "56912345678")
	if err != nil {
		t.Fatal(err)
	}
	if got.UnreadCount != 1 || got.Generation != 5 {
		t.Fatalf("unexpected summary: %#v", got)
	}
	if len(fake.writes) != 1 || len(fake.writes[0]) != 7 {
		t.Fatalf("writes = %d/%d, want one transaction with seven actions", len(fake.writes), len(fake.writes[0]))
	}
	assertMutationPutPK(t, fake.writes[0], "LIST_VERSION#")
	assertMutationPutPK(t, fake.writes[0], "SEARCH_VERSION#")
	assertMutationPutPK(t, fake.writes[0], "WORK_DUE#OUTBOX#")
}

func TestConversationMutationServiceDeleteIsIdempotentAndWritesTombstone(t *testing.T) {
	fixed := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name                    string
		items                   map[string]map[string]types.AttributeValue
		wantWrites, wantActions int
	}{
		{name: "missing conversation", items: map[string]map[string]types.AttributeValue{}, wantWrites: 0},
		{name: "existing conversation", items: mutationItems(t), wantWrites: 1, wantActions: 9},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &mutationDynamoFake{items: test.items}
			service, err := NewConversationMutationService(fake, "table", func() time.Time { return fixed })
			if err != nil {
				t.Fatal(err)
			}
			service.newID = func(time.Time) (string, error) { return "01K7J8QZD00000000000000000", nil }
			result, err := service.DeleteConversation(context.Background(), "56912345678")
			if err != nil {
				t.Fatal(err)
			}
			if !result.Deleted || len(fake.writes) != test.wantWrites {
				t.Fatalf("result=%#v writes=%d", result, len(fake.writes))
			}
			if test.wantWrites == 1 {
				if len(fake.writes[0]) != test.wantActions {
					t.Fatalf("actions=%d", len(fake.writes[0]))
				}
				assertMutationPutPK(t, fake.writes[0], "TOMBSTONE#56912345678")
				for _, write := range fake.writes[0] {
					if write.Put != nil {
						if pk, ok := write.Put.Item["pk"].(*types.AttributeValueMemberS); ok && pk.Value == "CONV#56912345678" {
							t.Fatal("delete transaction recreated conversation metadata")
						}
					}
				}
			}
		})
	}
}

func mutationItems(t *testing.T) map[string]map[string]types.AttributeValue {
	t.Helper()
	summary := ConversationSummary{ID: "56912345678", Phone: "56912345678", Alias: "Cliente", Channel: "whatsapp", Status: ConversationStatusBot, Generation: 4}
	conversation, err := attributevalue.MarshalMap(summary)
	if err != nil {
		t.Fatal(err)
	}
	listSequenceItem, _ := attributevalue.MarshalMap(listSequence{Sequence: 12})
	detailSequence, _ := attributevalue.MarshalMap(listSequence{Sequence: 7})
	return map[string]map[string]types.AttributeValue{"CONV#56912345678": conversation, listSequencePK: listSequenceItem, "SEQ#DETAIL#56912345678": detailSequence}
}
func assertMutationPutPK(t *testing.T, writes []types.TransactWriteItem, prefix string) {
	t.Helper()
	for _, write := range writes {
		if write.Put == nil {
			continue
		}
		pk, ok := write.Put.Item["pk"].(*types.AttributeValueMemberS)
		if ok && len(pk.Value) >= len(prefix) && pk.Value[:len(prefix)] == prefix {
			return
		}
	}
	t.Fatalf("transaction lacks put with PK prefix %q", prefix)
}
