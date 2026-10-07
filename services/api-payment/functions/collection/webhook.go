package collection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

// WebhookApp procesa solo notificaciones de cuotas, separado del simulador técnico.
type WebhookApp struct {
	Accounts Service
	Verifier PaymentVerifier
	Secret   string
	Zone     *time.Location
	Now      func() time.Time
}

func webhookResponse(status int) events.APIGatewayV2HTTPResponse {
	body := `{"received":false}`
	if status == 200 {
		body = `{"received":true}`
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: status, Body: body, Headers: map[string]string{"content-type": "application/json", "cache-control": "no-store", "x-content-type-options": "nosniff"}}
}

// Handle verifica la firma sobre bytes originales antes de leer identificadores o acceder a cuentas.
func (a WebhookApp) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if req.RequestContext.HTTP.Method != "POST" {
		return webhookResponse(405)
	}
	if len(req.Body) > 90*1024 {
		return webhookResponse(413)
	}
	if a.Now == nil || a.Zone == nil || a.Secret == "" || a.Verifier == nil {
		return webhookResponse(503)
	}
	body := []byte(req.Body)
	if req.IsBase64Encoded {
		var err error
		body, err = base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return webhookResponse(400)
		}
	}
	if len(body) > 64*1024 {
		return webhookResponse(413)
	}
	signature := ""
	for name, value := range req.Headers {
		if strings.EqualFold(name, "x-khipu-signature") {
			if signature != "" {
				return webhookResponse(401)
			}
			signature = value
		}
	}
	now := a.Now()
	if providers.VerifyKhipuSignature(body, signature, a.Secret, now) != nil {
		return webhookResponse(401)
	}
	var notification struct {
		PaymentID     string `json:"payment_id"`
		TransactionID string `json:"transaction_id"`
	}
	if json.Unmarshal(body, &notification) != nil {
		return webhookResponse(400)
	}
	if _, err := ulid.ParseStrict(notification.TransactionID); err != nil || !checkoutPaymentID.MatchString(notification.PaymentID) {
		return webhookResponse(400)
	}
	err := a.Accounts.ReceiveProviderNotification(ctx, ProviderNotification{PaymentID: notification.PaymentID, AttemptID: notification.TransactionID, ReceivedAt: now.UTC()})
	if err != nil {
		if errors.Is(err, ErrReplayMismatch) {
			return webhookResponse(409)
		}
		return webhookResponse(503)
	}
	// La confirmación es asíncrona: el stream consulta al proveedor y aplica el
	// dinero. El webhook solo reconoce recepción durable, nunca pago confirmado.
	return webhookResponse(200)
}
