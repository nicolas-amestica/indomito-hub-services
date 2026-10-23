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

func TestConversationMutationServiceDerivePersistsAndPublishes(t *testing.T) {
	fixed := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	fake := &mutationDynamoFake{items: mutationItems(t)}
	service, err := NewConversationMutationService(fake, "table", func() time.Time { return fixed })
	if err != nil {
		t.Fatal(err)
	}
	service.newID = func(time.Time) (string, error) { return "01K7J8QZD00000000000000000", nil }

	got, err := service.Derive(context.Background(), "56912345678", "  requiere especialista  ", "Atencion comercial")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ConversationStatusDerived || got.Reason == nil || *got.Reason != "requiere especialista" || got.ReasonLabel == nil || got.UnreadCount != 1 || got.Generation != 5 {
		t.Fatalf("unexpected derived summary: %#v", got)
	}
	if len(fake.writes) != 1 || len(fake.writes[0]) != 7 {
		t.Fatalf("writes = %d/%d, want one transaction with seven actions", len(fake.writes), len(fake.writes[0]))
	}
	foundDerivedEvent := false
	for _, write := range fake.writes[0] {
		if write.Put == nil {
			continue
		}
		if eventType, ok := write.Put.Item["eventType"].(*types.AttributeValueMemberS); ok && eventType.Value == "conversation.updated" {
			foundDerivedEvent = true
		}
	}
	if !foundDerivedEvent {
		t.Fatal("transaction lacks conversation.updated realtime event")
	}
}

func TestConversationMutationServiceActivateBotClearsTechnicalDerivation(t *testing.T) {
	fixed := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	reason, label := "agent_error", "Error del agente; requiere revision"
	items := mutationItems(t)
	summary := ConversationSummary{ID: "56912345678", Phone: "56912345678", Alias: "Cliente", Channel: "whatsapp", Status: ConversationStatusDerived, Reason: &reason, ReasonLabel: &label, Generation: 4}
	items["CONV#56912345678"], _ = attributevalue.MarshalMap(summary)
	fake := &mutationDynamoFake{items: items}
	service, err := NewConversationMutationService(fake, "table", func() time.Time { return fixed })
	if err != nil {
		t.Fatal(err)
	}
	service.newID = func(time.Time) (string, error) { return "01K7J8QZD00000000000000000", nil }

	got, err := service.ActivateBot(context.Background(), "56912345678")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ConversationStatusBot || got.Reason != nil || got.ReasonLabel != nil || got.Generation != 5 {
		t.Fatalf("unexpected active summary: %#v", got)
	}
	if len(fake.writes) != 1 || len(fake.writes[0]) != 7 {
		t.Fatalf("writes = %d/%d, want one transaction with seven actions", len(fake.writes), len(fake.writes[0]))
	}
}

func TestConversationMutationServicePublishesSequencedAgentProcessing(t *testing.T) {
	fixed := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		active        bool
		storedActive  bool
		storedToken   string
		requestToken  string
		wantWrites    int
		wantEventFlag bool
	}{
		{name: "start", active: true, requestToken: "wamid-1", wantWrites: 1, wantEventFlag: true},
		{name: "stop matching worker", active: false, storedActive: true, storedToken: "wamid-1", requestToken: "wamid-1", wantWrites: 1},
		{name: "stale worker cannot stop newer one", active: false, storedActive: true, storedToken: "wamid-2", requestToken: "wamid-1", wantWrites: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items := mutationItems(t)
			summary := ConversationSummary{ID: "56912345678", Phone: "56912345678", Alias: "Cliente", Channel: "whatsapp", Status: ConversationStatusBot, Generation: 4, AgentProcessing: test.storedActive, AgentProcessingID: test.storedToken}
			items["CONV#56912345678"], _ = attributevalue.MarshalMap(summary)
			fake := &mutationDynamoFake{items: items}
			service, err := NewConversationMutationService(fake, "table", func() time.Time { return fixed })
			if err != nil {
				t.Fatal(err)
			}
			service.newID = func(time.Time) (string, error) { return "01K7J8QZD00000000000000000", nil }
			if err = service.SetAgentProcessing(context.Background(), "56912345678", test.requestToken, test.active); err != nil {
				t.Fatal(err)
			}
			if len(fake.writes) != test.wantWrites {
				t.Fatalf("writes=%d, want %d", len(fake.writes), test.wantWrites)
			}
			if test.wantWrites == 0 {
				return
			}
			if len(fake.writes[0]) != 3 {
				t.Fatalf("actions=%d, want 3", len(fake.writes[0]))
			}
			foundEvent, foundMeta := false, false
			for _, write := range fake.writes[0] {
				if write.Put == nil {
					continue
				}
				if pk, ok := write.Put.Item["pk"].(*types.AttributeValueMemberS); ok && pk.Value == "CONV#56912345678" {
					var persisted ConversationSummary
					if err = attributevalue.UnmarshalMap(write.Put.Item, &persisted); err != nil {
						t.Fatal(err)
					}
					if persisted.AgentProcessing != test.wantEventFlag || (test.wantEventFlag && persisted.AgentProcessingUntil == nil) || (!test.wantEventFlag && persisted.AgentProcessingUntil != nil) {
						t.Fatalf("unexpected persisted processing state %#v", persisted)
					}
					foundMeta = true
				}
				eventType, ok := write.Put.Item["eventType"].(*types.AttributeValueMemberS)
				if !ok || eventType.Value != "agent_processing" {
					continue
				}
				payload := write.Put.Item["payload"].(*types.AttributeValueMemberM)
				active := payload.Value["active"].(*types.AttributeValueMemberBOOL)
				if active.Value != test.wantEventFlag {
					t.Fatalf("event active=%v, want %v", active.Value, test.wantEventFlag)
				}
				foundEvent = true
			}
			if !foundEvent || !foundMeta {
				t.Fatalf("transaction completeness event=%v meta=%v", foundEvent, foundMeta)
			}
		})
	}
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
