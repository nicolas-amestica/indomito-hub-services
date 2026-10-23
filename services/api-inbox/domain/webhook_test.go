package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type webhookFakeDynamo struct {
	items  map[string]map[string]types.AttributeValue
	writes []*dynamodb.TransactWriteItemsInput
	reads  map[string]int
}

func (f *webhookFakeDynamo) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	pk := input.Key["pk"].(*types.AttributeValueMemberS).Value
	sk := input.Key["sk"].(*types.AttributeValueMemberS).Value
	if f.reads == nil {
		f.reads = map[string]int{}
	}
	f.reads[pk+"|"+sk]++
	return &dynamodb.GetItemOutput{Item: f.items[pk+"|"+sk]}, nil
}

func (f *webhookFakeDynamo) TransactWriteItems(_ context.Context, input *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	f.writes = append(f.writes, input)
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func TestWebhookSignatureAndReservation(t *testing.T) {
	raw := []byte(`{"entry":[{"changes":[{"value":{"messages":[{"id":"wamid.1","from":"56912345678","type":"text","timestamp":"1","text":{"body":"hola"}}]}}]}]}`)
	secret := "0123456789abcdef"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	db := &webhookFakeDynamo{}
	service, err := NewWebhookService(db, "table", secret, func() time.Time { return time.Unix(100, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if !service.VerifySignature(raw, "sha256="+hex.EncodeToString(mac.Sum(nil))) || service.VerifySignature(append(raw, 'x'), "sha256="+hex.EncodeToString(mac.Sum(nil))) {
		t.Fatal("signature verification did not bind the exact raw body")
	}
	messages, err := DecodeInboundMetaMessages(raw)
	if err != nil || len(messages) != 1 || messages[0].Text != "hola" {
		t.Fatalf("unexpected decoded messages: %#v, %v", messages, err)
	}
	if err := service.Reserve(context.Background(), messages[0]); err != nil {
		t.Fatal(err)
	}
	if len(db.writes) != 1 || len(db.writes[0].TransactItems) != 2 {
		t.Fatal("reservation must atomically persist META_MSG and OUTBOX")
	}
}

func TestDecodeInboundMetaMessagesIgnoresIrrelevantEvents(t *testing.T) {
	messages, err := DecodeInboundMetaMessages([]byte(`{"entry":[{"changes":[{"value":{"statuses":[{"id":"wamid.1"}]}}]}]}`))
	if err != nil || len(messages) != 0 {
		t.Fatalf("irrelevant event should be ignored: %#v, %v", messages, err)
	}
}

func TestDecodeAndApplyInboundMetaDeliveryStatus(t *testing.T) {
	raw := []byte(`{"entry":[{"changes":[{"value":{"statuses":[{"id":"wamid.out.1","status":"failed","timestamp":"10","errors":[{"code":131026,"message":"Message undeliverable"}]}]}}]}]}`)
	statuses, err := DecodeInboundMetaStatuses(raw)
	if err != nil || len(statuses) != 1 || statuses[0].Status != DeliveryStatusFailed || statuses[0].Error != "131026: Message undeliverable" {
		t.Fatalf("unexpected statuses: %#v, %v", statuses, err)
	}
	delivery := DeliveryStatusSent
	message, _ := attributevalue.MarshalMap(Message{ID: "01MESSAGE", MessageSK: "MSG#01MESSAGE", Phone: "56912345678", Type: MessageTypeTeam, DeliveryStatus: &delivery})
	sequence, _ := attributevalue.MarshalMap(listSequence{Sequence: 4})
	db := &webhookFakeDynamo{items: map[string]map[string]types.AttributeValue{
		"WAMID#wamid.out.1|META":         {"phone": &types.AttributeValueMemberS{Value: "56912345678"}, "msgSk": &types.AttributeValueMemberS{Value: "MSG#01MESSAGE"}},
		"CONV#56912345678|MSG#01MESSAGE": message,
		"SEQ#DETAIL#56912345678|META":    sequence,
	}}
	service, err := NewWebhookService(db, "table", "0123456789abcdef", func() time.Time { return time.Unix(100, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ApplyStatus(context.Background(), statuses[0]); err != nil {
		t.Fatal(err)
	}
	if len(db.writes) != 1 || len(db.writes[0].TransactItems) != 3 {
		t.Fatalf("writes = %d, want delivery, sequence and realtime event transaction", len(db.writes))
	}
}

func TestDeliveryStatusProgressionIsMonotonic(t *testing.T) {
	tests := []struct {
		current, next DeliveryStatus
		want          bool
	}{
		{DeliveryStatusSent, DeliveryStatusDelivered, true},
		{DeliveryStatusSent, DeliveryStatusRead, true},
		{DeliveryStatusDelivered, DeliveryStatusRead, true},
		{DeliveryStatusRead, DeliveryStatusDelivered, false},
		{DeliveryStatusFailed, DeliveryStatusRead, false},
		{DeliveryStatusDelivered, DeliveryStatusFailed, true},
	}
	for _, test := range tests {
		current := test.current
		if got := shouldApplyDeliveryStatus(&current, test.next); got != test.want {
			t.Errorf("%s -> %s = %v, want %v", test.current, test.next, got, test.want)
		}
	}
}

func TestApplyStatusAcknowledgesUnknownAndDuplicateReceipts(t *testing.T) {
	read := DeliveryStatusRead
	message, _ := attributevalue.MarshalMap(Message{ID: "01MESSAGE", MessageSK: "MSG#01MESSAGE", Phone: "56912345678", Type: MessageTypeTeam, DeliveryStatus: &read})
	tests := []struct {
		name  string
		items map[string]map[string]types.AttributeValue
	}{
		{name: "unknown WAMID", items: map[string]map[string]types.AttributeValue{}},
		{name: "duplicate read", items: map[string]map[string]types.AttributeValue{
			"WAMID#wamid.out.1|META":         {"phone": &types.AttributeValueMemberS{Value: "56912345678"}, "msgSk": &types.AttributeValueMemberS{Value: "MSG#01MESSAGE"}},
			"CONV#56912345678|MSG#01MESSAGE": message,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &webhookFakeDynamo{items: test.items}
			service, err := NewWebhookService(db, "table", "0123456789abcdef", time.Now)
			if err != nil {
				t.Fatal(err)
			}
			service.sleep = func(time.Duration) {}
			if err = service.ApplyStatus(context.Background(), InboundMetaStatus{WhatsAppMessageID: "wamid.out.1", Status: DeliveryStatusRead}); err != nil {
				t.Fatal(err)
			}
			if len(db.writes) != 0 {
				t.Fatalf("unexpected writes: %d", len(db.writes))
			}
			if test.name == "unknown WAMID" && db.reads["WAMID#wamid.out.1|META"] != 2 {
				t.Fatalf("WAMID lookup attempts = %d, want 2", db.reads["WAMID#wamid.out.1|META"])
			}
		})
	}
}
