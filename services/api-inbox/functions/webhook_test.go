package functions

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

type webhookReceiverFake struct {
	validSignature bool
	reserved       []domain.InboundMetaMessage
	statuses       []domain.InboundMetaStatus
	reactions      []domain.InboundMetaReaction
}

func (f *webhookReceiverFake) ApplyReaction(_ context.Context, reaction domain.InboundMetaReaction) error {
	f.reactions = append(f.reactions, reaction)
	return nil
}

func (f *webhookReceiverFake) ApplyStatus(_ context.Context, status domain.InboundMetaStatus) error {
	f.statuses = append(f.statuses, status)
	return nil
}

func (f *webhookReceiverFake) VerifySignature(_ []byte, _ string) bool {
	return f.validSignature
}

func (f *webhookReceiverFake) Reserve(_ context.Context, message domain.InboundMetaMessage) error {
	f.reserved = append(f.reserved, message)
	return nil
}

func TestReceiveWhatsAppWebhookRejectsInvalidSignature(t *testing.T) {
	t.Parallel()

	receiver := &webhookReceiverFake{}
	response, err := ReceiveWhatsAppWebhook(context.Background(), receiver, events.APIGatewayV2HTTPRequest{
		Body:    `{"object":"whatsapp_business_account","entry":[]}`,
		Headers: map[string]string{"x-hub-signature-256": "sha256=invalid"},
	})
	if err != nil {
		t.Fatalf("ReceiveWhatsAppWebhook() error = %v", err)
	}
	if response.StatusCode != 401 {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
	if len(receiver.reserved) != 0 {
		t.Fatalf("reserved %d messages with an invalid signature", len(receiver.reserved))
	}
}

func TestReceiveWhatsAppWebhookIgnoresSignedNonMessageEvent(t *testing.T) {
	t.Parallel()

	receiver := &webhookReceiverFake{validSignature: true}
	response, err := ReceiveWhatsAppWebhook(context.Background(), receiver, events.APIGatewayV2HTTPRequest{
		Body:    `{"object":"whatsapp_business_account","entry":[]}`,
		Headers: map[string]string{"x-hub-signature-256": "sha256=valid"},
	})
	if err != nil {
		t.Fatalf("ReceiveWhatsAppWebhook() error = %v", err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if len(receiver.reserved) != 0 {
		t.Fatalf("reserved %d messages for a non-message event", len(receiver.reserved))
	}
}

func TestReceiveWhatsAppWebhookAppliesSignedDeliveryStatus(t *testing.T) {
	t.Parallel()

	receiver := &webhookReceiverFake{validSignature: true}
	response, err := ReceiveWhatsAppWebhook(context.Background(), receiver, events.APIGatewayV2HTTPRequest{
		Body:    `{"entry":[{"changes":[{"value":{"statuses":[{"id":"wamid.out.1","status":"read","timestamp":"10"}]}}]}]}`,
		Headers: map[string]string{"x-hub-signature-256": "sha256=valid"},
	})
	if err != nil {
		t.Fatalf("ReceiveWhatsAppWebhook() error = %v", err)
	}
	if response.StatusCode != 200 || len(receiver.statuses) != 1 || receiver.statuses[0].Status != domain.DeliveryStatusRead {
		t.Fatalf("response=%d statuses=%#v", response.StatusCode, receiver.statuses)
	}
}
