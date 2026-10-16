package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

type webhookFakeDynamo struct {
	writes []*dynamodb.TransactWriteItemsInput
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
