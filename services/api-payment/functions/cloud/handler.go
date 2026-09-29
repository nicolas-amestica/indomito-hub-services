package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

// Endpoint selecciona únicamente la capacidad compilada en cada función.
type Endpoint string

const (
	Configuration Endpoint = "configuration"
	Create        Endpoint = "create"
	Read          Endpoint = "read"
	Verify        Endpoint = "verify"
	Webhook       Endpoint = "webhook"
	Return        Endpoint = "return"
)

// Handler construye la entrada Lambda sin rutas ni estado del simulador.
func Handler(endpoint Endpoint) func(context.Context, events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		if endpoint == Return {
			return response(200, map[string]string{"message": "El pago está en verificación. Vuelve al portal para consultar su estado; esta página no confirma el pago."}), nil
		}
		if endpoint != Webhook {
			if _, err := lambdautil.UserIDFromContext(req); err != nil {
				return failure(401, "UNAUTHORIZED"), nil
			}
		}
		if len(req.Body) > 90*1024 {
			return failure(413, "BODY_TOO_LARGE"), nil
		}
		app, err := GetApp(ctx)
		if err != nil {
			return failure(503, "CONFIGURATION_UNAVAILABLE"), nil
		}
		return app.Handle(ctx, endpoint, req), nil
	}
}

func response(code int, data any) events.APIGatewayV2HTTPResponse {
	body, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		return failure(500, "SERIALIZATION_ERROR")
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: code, Body: string(body), Headers: map[string]string{
		"content-type": "application/json; charset=utf-8", "cache-control": "no-store", "x-content-type-options": "nosniff", "referrer-policy": "no-referrer", "content-security-policy": "default-src 'none'; frame-ancestors 'none'",
	}}
}
func failure(code int, value string) events.APIGatewayV2HTTPResponse {
	return response(code, map[string]string{"error": value})
}
func header(req events.APIGatewayV2HTTPRequest, name string) string {
	for k, v := range req.Headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}
func validID(id string) bool { _, err := ulid.ParseStrict(id); return err == nil }

// Handle aplica identidad del authorizer, validación y propiedad en cada operación.
func (a *App) Handle(ctx context.Context, endpoint Endpoint, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if endpoint == Webhook {
		return a.webhook(ctx, req)
	}
	owner, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		return failure(401, "UNAUTHORIZED")
	}
	switch endpoint {
	case Configuration:
		_, err := a.Gateway.DevelopmentBank(ctx)
		if err != nil {
			return failure(503, "DEVELOPMENT_ACCOUNT_NOT_VERIFIED")
		}
		return response(200, map[string]any{"mode": "khipu-development", "testBankAvailable": true, "amount": 20000, "currency": "CLP"})
	case Create:
		return a.create(ctx, req, owner)
	case Read, Verify:
		id := req.PathParameters["id"]
		if !validID(id) {
			return failure(400, "INVALID_ID")
		}
		v, err := a.get(ctx, id)
		if errors.Is(err, errNotFound) || (err == nil && v.Owner != owner) {
			return failure(404, "NOT_FOUND")
		}
		if err != nil {
			return failure(503, "STORAGE_UNAVAILABLE")
		}
		if endpoint == Verify && v.PaymentID != "" && v.Status != "CONFIRMED" {
			return a.confirm(ctx, v, v.PaymentID)
		}
		return response(200, v)
	default:
		return failure(404, "NOT_FOUND")
	}
}

func (a *App) create(ctx context.Context, req events.APIGatewayV2HTTPRequest, owner string) events.APIGatewayV2HTTPResponse {
	id := header(req, "Idempotency-Key")
	if !validID(id) || req.IsBase64Encoded || strings.TrimSpace(req.Body) != "{}" {
		return failure(400, "EMPTY_BODY_AND_ULID_KEY_REQUIRED")
	}
	previous, err := a.get(ctx, id)
	if err == nil {
		if previous.Owner != owner {
			return failure(409, "KEY_CONFLICT")
		}
		return response(200, previous)
	}
	if !errors.Is(err, errNotFound) {
		return failure(503, "STORAGE_UNAVAILABLE")
	}
	bank, err := a.Gateway.DevelopmentBank(ctx)
	if err != nil {
		return failure(503, "DEVELOPMENT_ACCOUNT_NOT_VERIFIED")
	}
	v := Attempt{PK: "TEST#" + id, SK: "ATTEMPT", ID: id, Owner: owner, Amount: 20000, Status: "CREATING", CreatedAt: a.Now().UTC().Format(time.RFC3339)}
	if err = a.insert(ctx, v); err != nil {
		if conditional(err) {
			return failure(409, "REQUEST_IN_PROGRESS")
		}
		return failure(503, "STORAGE_UNAVAILABLE")
	}
	if err = a.quota(ctx, owner); err != nil {
		v.Status = "BLOCKED"
		if saveErr := a.replace(ctx, v, "CREATING"); saveErr != nil {
			return failure(503, "STORAGE_UNAVAILABLE")
		}
		if conditional(err) {
			return failure(429, "DAILY_TEST_LIMIT")
		}
		return failure(503, "STORAGE_UNAVAILABLE")
	}
	out, err := a.Gateway.Create(ctx, providers.CheckoutRequest{BankID: bank, TransactionID: id, Subject: "Prueba DEV Giras Indómito — sin dinero real", Amount: v.Amount,
		ReturnURL: a.BaseURL + "/pagos/retorno", CancelURL: a.BaseURL + "/pagos/retorno", NotifyURL: a.BaseURL + "/pagos/khipu/notificaciones", ExpiresAt: a.Now().Add(time.Hour)})
	if err != nil {
		v.Status = "RECONCILIATION_REQUIRED"
		if saveErr := a.replace(ctx, v, "CREATING"); saveErr != nil && !conditional(saveErr) {
			return failure(503, "STORAGE_UNAVAILABLE")
		}
		return failure(502, "RECONCILIATION_REQUIRED_DO_NOT_RECREATE")
	}
	v.PaymentID = out.PaymentID
	v.PaymentURL = out.PaymentURL
	v.Status = "PENDING"
	if err = a.replace(ctx, v, "CREATING"); err != nil {
		if conditional(err) {
			current, readErr := a.get(ctx, id)
			if readErr == nil && current.Status == "CONFIRMED" && current.PaymentID == out.PaymentID {
				return response(200, current)
			}
		}
		return failure(503, "RECONCILIATION_REQUIRED_DO_NOT_RECREATE")
	}
	return response(201, v)
}

func (a *App) confirm(ctx context.Context, v Attempt, providerID string) events.APIGatewayV2HTTPResponse {
	if v.Status == "CONFIRMED" {
		if v.PaymentID != providerID {
			return failure(409, "PAYMENT_MISMATCH")
		}
		return response(200, v)
	}
	if v.Status != "PENDING" && v.Status != "CREATING" && v.Status != "RECONCILIATION_REQUIRED" {
		return failure(409, "INVALID_STATE")
	}
	if v.PaymentID != "" && v.PaymentID != providerID {
		return failure(409, "PAYMENT_MISMATCH")
	}
	verified, err := a.Gateway.Verify(ctx, providerID, v.ID, v.Amount)
	if errors.Is(err, providers.ErrVerification) {
		return failure(409, "PAYMENT_NOT_CONFIRMED")
	}
	if err != nil {
		return failure(503, "PROVIDER_UNAVAILABLE")
	}
	previous := v.Status
	v.Status = "CONFIRMED"
	v.PaymentID = providerID
	v.Receipt = &Receipt{ID: ulid.Make().String(), Amount: v.Amount, IssuedAt: verified.ConciliationDate.UTC().Format(time.RFC3339), Description: "Comprobante de prueba Khipu DEV. Sin dinero real. No es boleta ni factura SII."}
	v.Event = &PaymentEvent{ID: ulid.Make().String(), Version: 1, Type: "payment.test-confirmed", OccurredAt: a.Now().UTC().Format(time.RFC3339)}
	v.NotificationStatus = "PENDING_WORKER"
	if err = a.replace(ctx, v, previous); err != nil {
		if conditional(err) {
			current, readErr := a.get(ctx, v.ID)
			if readErr == nil && current.Status == "CONFIRMED" && current.PaymentID == providerID {
				return response(200, current)
			}
		}
		return failure(503, "STORAGE_UNAVAILABLE")
	}
	return response(200, v)
}

func (a *App) webhook(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if len(req.Body) > 90*1024 {
		return failure(413, "BODY_TOO_LARGE")
	}
	body := []byte(req.Body)
	if req.IsBase64Encoded {
		var err error
		body, err = base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return failure(400, "INVALID_BODY")
		}
	}
	if providers.VerifyKhipuSignature(body, header(req, "x-khipu-signature"), a.WebhookSecret, a.Now()) != nil {
		return failure(401, "INVALID_SIGNATURE")
	}
	var event struct {
		PaymentID     string `json:"payment_id"`
		TransactionID string `json:"transaction_id"`
	}
	if json.Unmarshal(body, &event) != nil || !validID(event.TransactionID) || len(event.PaymentID) != 12 {
		return failure(400, "INVALID_EVENT")
	}
	v, err := a.get(ctx, event.TransactionID)
	if errors.Is(err, errNotFound) {
		return failure(404, "UNKNOWN_PAYMENT")
	}
	if err != nil {
		return failure(503, "STORAGE_UNAVAILABLE")
	}
	result := a.confirm(ctx, v, event.PaymentID)
	if result.StatusCode == 200 {
		return response(200, map[string]bool{"received": true})
	}
	return result
}
