package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestDecodeInboundMetaReactionsSeparatesReactionFromMessages(t *testing.T) {
	raw := []byte(`{"entry":[{"changes":[{"value":{"messages":[{"id":"wamid.event","from":"56912345678","type":"reaction","reaction":{"message_id":"wamid.target","emoji":"❤️"}}]}}]}]}`)
	reactions, err := DecodeInboundMetaReactions(raw)
	if err != nil || len(reactions) != 1 || reactions[0].MessageID != "wamid.target" || reactions[0].Emoji != "❤️" {
		t.Fatalf("unexpected reactions %#v err=%v", reactions, err)
	}
	messages, err := DecodeInboundMetaMessages(raw)
	if err != nil || len(messages) != 0 {
		t.Fatalf("reaction must not become inbound message: %#v err=%v", messages, err)
	}
}

func TestDecodeInboundMetaMessagesPreservesRichContent(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"entry":[{"changes":[{"value":{"messages":[{"id":"wamid.location","from":"56912345678","type":"location","timestamp":"1","location":{"latitude":-33.4,"longitude":-70.6,"name":"Oficina","address":"Santiago"}},{"id":"wamid.contact","from":"56912345678","type":"contacts","timestamp":"2","contacts":[{"name":{"formatted_name":"Ana"},"phones":[{"phone":"+56911111111"}],"emails":[{"email":"ana@example.com"}],"org":{"company":"Indomito"}}]},{"id":"wamid.interactive","from":"56912345678","type":"interactive","timestamp":"3","interactive":{"type":"list_reply","list_reply":{"id":"tour-1","title":"Tour 1","description":"Seleccionado"}}},{"id":"wamid.sticker","from":"56912345678","type":"sticker","timestamp":"4","sticker":{"id":"media-1","mime_type":"image/webp","sha256":"digest"}}]}}]}]}`)
	messages, err := DecodeInboundMetaMessages(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 || messages[0].Rich == nil || messages[0].Rich.Location == nil || messages[0].Rich.Location.Name != "Oficina" {
		t.Fatalf("unexpected location %#v", messages)
	}
	if messages[1].Rich == nil || len(messages[1].Rich.Contacts) != 1 || messages[1].Rich.Contacts[0].Email != "ana@example.com" {
		t.Fatalf("unexpected contacts %#v", messages[1])
	}
	if messages[2].Text != "Tour 1" || messages[2].Rich == nil || messages[2].Rich.InteractiveReply.ID != "tour-1" {
		t.Fatalf("unexpected interactive reply %#v", messages[2])
	}
	if messages[3].MediaID != "media-1" || messages[3].Rich == nil || messages[3].Rich.Kind != "sticker" {
		t.Fatalf("unexpected sticker %#v", messages[3])
	}
}

func TestDecodeInboundMetaMessagesPreservesAdReferral(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"entry":[{"changes":[{"value":{"messages":[{"id":"wamid.ad","from":"56912345678","type":"text","timestamp":"1","text":{"body":"Hola"},"referral":{"source_url":"https://fb.me/ad","source_id":"1200001","source_type":"ad","headline":"Gira a Bariloche","body":"Cotiza hoy","media_type":"image","ctwa_clid":"click-secret"}}]}}]}]}`)
	messages, err := DecodeInboundMetaMessages(raw)
	if err != nil || len(messages) != 1 {
		t.Fatalf("unexpected messages %#v err=%v", messages, err)
	}
	referral := messages[0].Referral
	if referral == nil || referral.SourceID != "1200001" || referral.SourceType != "ad" || referral.Headline != "Gira a Bariloche" || referral.CtwaClid != "click-secret" {
		t.Fatalf("unexpected referral %#v", referral)
	}
}

func TestDecodeInboundMetaMessagesMakesUnsupportedContentVisible(t *testing.T) {
	raw := []byte(`{"entry":[{"changes":[{"value":{"messages":[{"id":"wamid.unsupported","from":"56912345678","type":"order","timestamp":"1"}]}}]}]}`)
	messages, err := DecodeInboundMetaMessages(raw)
	if err != nil || len(messages) != 1 {
		t.Fatalf("unexpected unsupported decode: %#v err=%v", messages, err)
	}
	if messages[0].Text != "[Contenido de WhatsApp no compatible: order]" || messages[0].Type != "order" {
		t.Fatalf("unsupported content must remain unequivocal and auditable: %#v", messages[0])
	}
}

type webhookFakeDynamo struct {
	items               map[string]map[string]types.AttributeValue
	writes              []*dynamodb.TransactWriteItemsInput
	reads               map[string]int
	transactionFailures int
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
	if f.transactionFailures > 0 {
		f.transactionFailures--
		code := "ConditionalCheckFailed"
		return nil, &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: &code}}}
	}
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

func TestWebhookDecodersSustainDuplicateDelayedAndMixedCallbackLoad(t *testing.T) {
	const callbacks = 1_000
	messages := make([]map[string]any, 0, callbacks*2)
	statuses := make([]map[string]any, 0, callbacks*3)
	for index := 0; index < callbacks; index++ {
		wamid := fmt.Sprintf("wamid.load.%04d", index)
		messages = append(messages,
			map[string]any{"id": wamid, "from": "56912345678", "type": "text", "timestamp": "1", "text": map[string]string{"body": "carga sintética"}},
			map[string]any{"id": wamid + ".reaction", "from": "56912345678", "type": "reaction", "timestamp": "2", "reaction": map[string]string{"message_id": wamid, "emoji": "👍"}},
		)
		// Intentionally delayed and out of order, including a duplicate read.
		statuses = append(statuses,
			map[string]any{"id": wamid, "status": "read", "timestamp": "4"},
			map[string]any{"id": wamid, "status": "delivered", "timestamp": "3"},
			map[string]any{"id": wamid, "status": "read", "timestamp": "4"},
		)
	}
	envelope := map[string]any{
		"entry": []any{
			map[string]any{
				"changes": []any{
					map[string]any{
						"value": map[string]any{"messages": messages, "statuses": statuses},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decodedMessages, err := DecodeInboundMetaMessages(raw)
	if err != nil || len(decodedMessages) != callbacks {
		t.Fatalf("messages = %d, want %d, err=%v", len(decodedMessages), callbacks, err)
	}
	decodedReactions, err := DecodeInboundMetaReactions(raw)
	if err != nil || len(decodedReactions) != callbacks {
		t.Fatalf("reactions = %d, want %d, err=%v", len(decodedReactions), callbacks, err)
	}
	decodedStatuses, err := DecodeInboundMetaStatuses(raw)
	if err != nil || len(decodedStatuses) != callbacks*3 {
		t.Fatalf("statuses = %d, want %d, err=%v", len(decodedStatuses), callbacks*3, err)
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
	if len(db.writes) != 1 || len(db.writes[0].TransactItems) != 4 {
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

func TestApplyStatusRetriesCanceledRealtimeTransaction(t *testing.T) {
	delivery := DeliveryStatusSent
	message, err := attributevalue.MarshalMap(Message{ID: "01MESSAGE", MessageSK: "MSG#01MESSAGE", Phone: "56912345678", Type: MessageTypeTeam, DeliveryStatus: &delivery})
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := attributevalue.MarshalMap(listSequence{Sequence: 4})
	if err != nil {
		t.Fatal(err)
	}
	db := &webhookFakeDynamo{transactionFailures: 1, items: map[string]map[string]types.AttributeValue{
		"WAMID#wamid.out.1|META":         {"phone": &types.AttributeValueMemberS{Value: "56912345678"}, "msgSk": &types.AttributeValueMemberS{Value: "MSG#01MESSAGE"}},
		"CONV#56912345678|MSG#01MESSAGE": message,
		"SEQ#DETAIL#56912345678|META":    sequence,
	}}
	service, err := NewWebhookService(db, "table", "0123456789abcdef", func() time.Time { return time.Unix(100, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ApplyStatus(context.Background(), InboundMetaStatus{WhatsAppMessageID: "wamid.out.1", Status: DeliveryStatusDelivered}); err != nil {
		t.Fatal(err)
	}
	if len(db.writes) != 2 {
		t.Fatalf("transaction attempts = %d, want 2", len(db.writes))
	}
	for index, write := range db.writes {
		if len(write.TransactItems) != 4 {
			t.Fatalf("attempt %d items = %d, want 4", index, len(write.TransactItems))
		}
		sequenceValue := write.TransactItems[2].Put.Item["sequence"].(*types.AttributeValueMemberN).Value
		if sequenceValue != "5" {
			t.Fatalf("attempt %d realtime sequence = %s, want 5", index, sequenceValue)
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
			wantWrites := 0
			if test.name == "unknown WAMID" {
				wantWrites = 1
			}
			if len(db.writes) != wantWrites {
				t.Fatalf("writes = %d, want %d", len(db.writes), wantWrites)
			}
			if test.name == "unknown WAMID" && db.reads["WAMID#wamid.out.1|META"] != 3 {
				t.Fatalf("WAMID lookup attempts = %d, want 3", db.reads["WAMID#wamid.out.1|META"])
			}
		})
	}
}
