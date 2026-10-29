package domain

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type inboundFake struct {
	items  map[string]map[string]types.AttributeValue
	writes [][]types.TransactWriteItem
}

func (f *inboundFake) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	pk := input.Key["pk"].(*types.AttributeValueMemberS).Value
	return &dynamodb.GetItemOutput{Item: f.items[pk]}, nil
}

func (f *inboundFake) TransactWriteItems(_ context.Context, input *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	f.writes = append(f.writes, input.TransactItems)
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func TestInboundProcessorMaterializesNewConversationAtomically(t *testing.T) {
	fixed := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	reservation, err := attributevalue.MarshalMap(inboundRecord{MessageID: "wamid.1", Phone: "56912345678", MessageType: "text", ProviderTimestamp: "1791980000", Text: "Hola", State: "reserved", Referral: &MetaReferral{SourceID: "1200001", SourceType: "ad", Headline: "Gira a Bariloche", CtwaClid: "click-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	fake := &inboundFake{items: map[string]map[string]types.AttributeValue{"META_MSG#wamid.1": reservation}}
	processor, err := NewInboundProcessor(fake, "table", func() time.Time { return fixed })
	if err != nil {
		t.Fatal(err)
	}
	if err = processor.Process(context.Background(), "wamid.1"); err != nil {
		t.Fatal(err)
	}
	if len(fake.writes) != 1 || len(fake.writes[0]) != 12 {
		t.Fatalf("expected one twelve-item transaction, got %#v", fake.writes)
	}
	for _, prefix := range []string{"CONV#56912345678", "LIST_VERSION#", "SEARCH_VERSION#", "WORK_DUE#OUTBOX#", "WORK_DUE#AGENT#"} {
		assertMutationPutPK(t, fake.writes[0], prefix)
	}
	if !hasInboundMessage(fake.writes[0], "wamid.1") || !hasProcessedReservation(fake.writes[0]) || !hasActiveCountUpdate(fake.writes[0]) {
		t.Fatal("transaction must include message, reservation completion and active counter")
	}
	expiresAt := time.Unix(1791980000, 0).UTC().Add(24 * time.Hour)
	if !hasServiceWindowExpiry(fake.writes[0], expiresAt) {
		t.Fatalf("conversation must expose service window expiry %s", expiresAt)
	}
	if !hasMetaReferral(fake.writes[0], "1200001", "click-secret") {
		t.Fatal("conversation and message must preserve Meta ad attribution")
	}
	if outboxContainsMetaClickID(fake.writes[0]) {
		t.Fatal("realtime payload must not expose ctwa_clid")
	}
}

func hasMetaReferral(writes []types.TransactWriteItem, sourceID, ctwaClid string) bool {
	found := 0
	for _, write := range writes {
		if write.Put == nil {
			continue
		}
		value, ok := write.Put.Item["referral"].(*types.AttributeValueMemberM)
		if !ok {
			continue
		}
		id, idOK := value.Value["sourceId"].(*types.AttributeValueMemberS)
		click, clickOK := value.Value["ctwaClid"].(*types.AttributeValueMemberS)
		if idOK && clickOK && id.Value == sourceID && click.Value == ctwaClid {
			found++
		}
	}
	return found >= 2
}

func outboxContainsMetaClickID(writes []types.TransactWriteItem) bool {
	for _, write := range writes {
		if write.Put == nil {
			continue
		}
		payload, ok := write.Put.Item["payload"].(*types.AttributeValueMemberM)
		if !ok {
			continue
		}
		conversation, ok := payload.Value["conversation"].(*types.AttributeValueMemberM)
		if !ok {
			continue
		}
		referral, ok := conversation.Value["referral"].(*types.AttributeValueMemberM)
		if ok && referral.Value["ctwaClid"] != nil {
			return true
		}
	}
	return false
}

func hasServiceWindowExpiry(writes []types.TransactWriteItem, expected time.Time) bool {
	for _, write := range writes {
		if write.Put == nil {
			continue
		}
		value, ok := write.Put.Item["serviceWindowExpiresAt"].(*types.AttributeValueMemberS)
		if ok && value.Value == expected.Format(time.RFC3339Nano) {
			return true
		}
	}
	return false
}

func TestInboundProcessorSkipsProcessedReservation(t *testing.T) {
	reservation, _ := attributevalue.MarshalMap(inboundRecord{MessageID: "wamid.1", Phone: "56912345678", State: "processed"})
	fake := &inboundFake{items: map[string]map[string]types.AttributeValue{"META_MSG#wamid.1": reservation}}
	processor, _ := NewInboundProcessor(fake, "table", time.Now)
	if err := processor.Process(context.Background(), "wamid.1"); err != nil || len(fake.writes) != 0 {
		t.Fatalf("processed reservation must be idempotent: %v %#v", err, fake.writes)
	}
}

func hasInboundMessage(writes []types.TransactWriteItem, wamid string) bool {
	for _, write := range writes {
		if write.Put == nil {
			continue
		}
		value, ok := write.Put.Item["whatsappMessageId"].(*types.AttributeValueMemberS)
		if ok && value.Value == wamid {
			return true
		}
	}
	return false
}

func hasProcessedReservation(writes []types.TransactWriteItem) bool {
	for _, write := range writes {
		if write.Update != nil && write.Update.ExpressionAttributeValues[":processed"] != nil {
			return true
		}
	}
	return false
}

func hasActiveCountUpdate(writes []types.TransactWriteItem) bool {
	for _, write := range writes {
		if write.Update == nil {
			continue
		}
		pk, ok := write.Update.Key["pk"].(*types.AttributeValueMemberS)
		if ok && pk.Value == "CONV_COUNT" {
			return true
		}
	}
	return false
}
