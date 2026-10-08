package collection

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
)

type PublicReceiptVerification struct {
	Authentic     bool   `json:"authentic"`
	Code          string `json:"code,omitempty"`
	Amount        int64  `json:"amount,omitempty"`
	EffectiveDate string `json:"effectiveDate,omitempty"`
	Concept       string `json:"concept,omitempty"`
	Status        string `json:"status,omitempty"`
	Version       int64  `json:"version,omitempty"`
}

// HandleReceiptVerification valida un comprobante por su clave directa sin exponer identidad.
func (a PublicApp) HandleReceiptVerification(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.RequestContext.HTTP.Method != "POST" {
		return portalResponse(405, PublicReceiptVerification{Authentic: false}), nil
	}
	ip, err := networkIdentity(req.RequestContext.HTTP.SourceIP)
	if err != nil || a.consumeAttempt(ctx, "receipt-verification-ip:v1", ip, 30, time.Now().UTC()) != nil {
		return portalResponse(429, PublicReceiptVerification{Authentic: false}), nil
	}
	var body struct {
		Code string `json:"code"`
	}
	if len(req.Body) > 256 || decodeAdmin(req, &body) != nil {
		return portalResponse(200, PublicReceiptVerification{Authentic: false}), nil
	}
	code := strings.ToUpper(strings.TrimSpace(body.Code))
	if !validPortalID(code) {
		return portalResponse(200, PublicReceiptVerification{Authentic: false}), nil
	}
	row, err := a.Accounts.read(ctx, "RECEIPT#"+code, "META")
	if errors.Is(err, ErrNotFound) {
		return portalResponse(200, PublicReceiptVerification{Authentic: false}), nil
	}
	if err != nil || row.Event == nil || row.ReceiptID != code || row.DocumentSHA256 == "" {
		return portalResponse(503, PublicReceiptVerification{Authentic: false}), nil
	}
	status := "REGISTERED"
	concept := "Pago recibido"
	if row.Event.Type == "GROUP_DEPOSIT_RECEIVED" {
		concept = "Abono grupal recibido y asignado"
	} else if row.Event.Type == "DEPOSIT_RECEIVED" {
		concept = "Abono recibido"
	} else if row.Event.Type == "PAYMENT_REQUIRES_REVIEW" {
		status, concept = "UNDER_REVIEW", "Dinero recibido pendiente de revisión"
	}
	if row.Event.AttemptID != "" {
		resolution, readErr := a.Accounts.read(ctx, "ATTEMPT#"+row.Event.AttemptID, "RESOLUTION")
		if readErr == nil && resolution.Event != nil && resolution.Event.Type == "PAYMENT_REVERSED_BY_PROVIDER" {
			status = "REVERSED"
		} else if readErr != nil && !errors.Is(readErr, ErrNotFound) {
			return portalResponse(503, PublicReceiptVerification{Authentic: false}), nil
		}
	}
	return lambdautil.SuccessResponseWithHeaders(200, PublicReceiptVerification{Authentic: true, Code: code, Amount: row.Event.Amount, EffectiveDate: row.Event.EffectiveDate, Concept: concept, Status: status, Version: receiptDocumentVersion(row)}, map[string]string{"cache-control": "no-store", "referrer-policy": "no-referrer"})
}

func ReceiptVerificationHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getPublicApp(ctx)
	if err != nil {
		return portalResponse(503, PublicReceiptVerification{Authentic: false}), nil
	}
	return app.HandleReceiptVerification(ctx, req)
}
