package collection

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/oklog/ulid/v2"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// PortalApp coordina acciones de una cuenta autorizada sin recibir importes del navegador.
type PortalApp struct {
	Accounts        Service
	Gateway         CheckoutGateway
	URLs            CheckoutURLs
	Now             func() time.Time
	CheckoutEnabled bool
}

// PortalAttempt excluye correo, referencias bancarias y datos internos de la cuenta.
type PortalAttempt struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	PaymentURL   string `json:"paymentUrl,omitempty"`
	ReceiptReady bool   `json:"receiptReady,omitempty"`
}

func portalResponse(status int, data any) events.APIGatewayV2HTTPResponse {
	body, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		return webhookResponse(503)
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: status, Body: string(body), Headers: map[string]string{"content-type": "application/json", "cache-control": "no-store", "referrer-policy": "no-referrer"}}
}
func portalFailure(status int) events.APIGatewayV2HTTPResponse {
	return portalResponse(status, map[string]string{"error": "No fue posible procesar la solicitud. Consulta el estado antes de volver a intentar."})
}

func validPortalID(id string) bool {
	parsed, err := ulid.ParseStrict(id)
	return err == nil && parsed.String() == id
}

// HandleCheckout valida sesión y correo, reserva y crea un único checkout DEV.
func (a PortalApp) HandleCheckout(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if req.RequestContext.HTTP.Method != "POST" {
		return portalFailure(405)
	}
	if a.Now == nil || a.Gateway == nil {
		return portalFailure(503)
	}
	var body struct {
		Email string `json:"email"`
	}
	if len(req.Body) > 1024 || decodeAdmin(req, &body) != nil {
		return portalFailure(400)
	}
	address, err := mail.ParseAddress(body.Email)
	if err != nil || address.Address != body.Email || len(body.Email) > 254 {
		return portalFailure(400)
	}
	requestID := ""
	for k, v := range req.Headers {
		if strings.EqualFold(k, "Idempotency-Key") {
			if requestID != "" {
				return portalFailure(400)
			}
			requestID = v
		}
	}
	if !validPortalID(requestID) {
		return portalFailure(400)
	}
	account, err := a.Accounts.PassengerAccount(ctx, req)
	if err != nil {
		return portalFailure(403)
	}
	if !account.Active || account.Free {
		return portalFailure(409)
	}
	sessionID, _ := req.RequestContext.Authorizer.Lambda["paymentSessionId"].(string)
	if !validPortalID(sessionID) {
		return portalFailure(403)
	}
	id := paymentOperationID("checkout:" + account.ID + ":" + requestID)
	stored, err := a.Accounts.read(ctx, "ATTEMPT#"+id, "META")
	if errors.Is(err, ErrNotFound) {
		audit := domain.Audit{CommandID: id, Actor: "passenger:" + account.ID, Reason: "Solicitud de pago de cuota completa", RecordedAt: a.Now().UTC()}
		_, reserveErr := a.Accounts.ReserveCheckout(ctx, account.ID, account.Version, audit, id, sessionID, body.Email)
		if reserveErr != nil && !errors.Is(reserveErr, domain.ErrConflict) && !errors.Is(reserveErr, ErrReplayMismatch) {
			return portalFailure(503)
		}
		// Recupera el resultado reservado, incluido un comando concurrente idéntico.
		stored, err = a.Accounts.read(ctx, "ATTEMPT#"+id, "META")
	}
	if err != nil {
		return portalFailure(409)
	}
	if stored.Attempt == nil || stored.Attempt.AccountID != account.ID || stored.Attempt.Email != body.Email {
		return portalFailure(409)
	}
	// Una repetición después de confirmación no inicia el cobro de la cuota siguiente.
	if current, readErr := a.attemptView(ctx, account, id); readErr == nil && (current.Status == "CONFIRMED" || current.Status == "REVIEW_REQUIRED") {
		return portalResponse(200, current)
	}
	if account.RequiresPaymentReview() {
		return portalFailure(409)
	}
	_, err = a.Accounts.CreateDevelopmentCheckout(ctx, a.Gateway, account.ID, id, "passenger:"+account.ID, a.URLs, a.Now())
	if err != nil {
		if errors.Is(err, ErrDispatchUncertain) {
			return portalResponse(202, PortalAttempt{ID: id, Status: "RECONCILIATION_REQUIRED"})
		}
		return portalFailure(503)
	}
	account, err = a.Accounts.GetAccount(ctx, account.ID)
	if err != nil {
		return portalFailure(503)
	}
	current, err := a.attemptView(ctx, account, id)
	if err != nil {
		return portalFailure(503)
	}
	return portalResponse(200, current)
}

// HandleAccount refresca la lista de cuotas usando exclusivamente la cuenta de la sesión breve.
func (a PortalApp) HandleAccount(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if req.RequestContext.HTTP.Method != "GET" {
		return portalFailure(405)
	}
	account, err := a.Accounts.PassengerAccount(ctx, req)
	if err != nil {
		return portalFailure(403)
	}
	return portalResponse(200, publicAccountView(account, a.CheckoutEnabled))
}

func (a PortalApp) attemptView(ctx context.Context, account domain.Account, id string) (PortalAttempt, error) {
	r, err := a.Accounts.read(ctx, "ATTEMPT#"+id, "META")
	if err != nil {
		return PortalAttempt{}, err
	}
	if r.Attempt == nil || r.Attempt.AccountID != account.ID || r.Attempt.ID != id {
		return PortalAttempt{}, ErrNotFound
	}
	view := PortalAttempt{ID: id, Status: "RECONCILIATION_REQUIRED"}
	resolution, resolutionErr := a.Accounts.read(ctx, "ATTEMPT#"+id, "RESOLUTION")
	if resolutionErr == nil {
		if resolution.AccountID != account.ID || resolution.Event == nil || resolution.Event.AttemptID != id {
			return PortalAttempt{}, ErrNotFound
		}
		switch resolution.Event.Type {
		case "PAYMENT_ATTEMPT_RESOLVED_UNPAID":
			view.Status = "UNPAID_FINAL"
		case "PAYMENT_REVERSED_BY_PROVIDER":
			view.Status = "REVERSED"
		default:
			return PortalAttempt{}, ErrReplayMismatch
		}
		return view, nil
	}
	if !errors.Is(resolutionErr, ErrNotFound) {
		return PortalAttempt{}, resolutionErr
	}
	outcome, outcomeErr := a.Accounts.read(ctx, "ATTEMPT#"+id, "OUTCOME")
	if outcomeErr == nil {
		if outcome.AccountID != account.ID || outcome.Event == nil || outcome.Event.AccountID != account.ID || outcome.Event.AttemptID != id {
			return PortalAttempt{}, ErrNotFound
		}
		switch outcome.Event.Type {
		case "PAYMENT_RECEIVED":
			view.Status = "CONFIRMED"
			view.ReceiptReady = a.receiptReady(ctx, account.ID, id, outcome.Event)
		case "PAYMENT_REQUIRES_REVIEW":
			view.Status = "REVIEW_REQUIRED"
		default:
			return PortalAttempt{}, ErrReplayMismatch
		}
		return view, nil
	}
	if !errors.Is(outcomeErr, ErrNotFound) {
		return PortalAttempt{}, outcomeErr
	}
	checkout, err := a.Accounts.read(ctx, "ATTEMPT#"+id, "CHECKOUT")
	if errors.Is(err, ErrNotFound) {
		return view, nil
	}
	if err != nil {
		return PortalAttempt{}, err
	}
	if checkout.AccountID != account.ID || checkout.Checkout == nil {
		return PortalAttempt{}, ErrNotFound
	}
	ref := "khipu:" + checkout.Checkout.PaymentID
	c, err := a.Accounts.confirmedPayment(ctx, paymentOperationID(ref), account.ID, ref)
	if err == nil {
		view.Status = "CONFIRMED"
		if c.Event.Type == "PAYMENT_REQUIRES_REVIEW" {
			view.Status = "REVIEW_REQUIRED"
		} else {
			view.ReceiptReady = a.receiptReady(ctx, account.ID, id, &c.Event)
		}
		return view, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return PortalAttempt{}, err
	}
	if account.Active && !account.RequiresPaymentReview() && account.OpenAttemptID == id && checkout.Checkout.ExpiresAt.After(a.Now()) {
		view.Status = "PENDING_PAYMENT"
		view.PaymentURL = checkout.Checkout.PaymentURL
	}
	return view, nil
}

func (a PortalApp) receiptReady(ctx context.Context, accountID, attemptID string, event *domain.Event) bool {
	if event == nil || event.Type != "PAYMENT_RECEIVED" || event.AccountID != accountID || event.AttemptID != attemptID || !strings.HasPrefix(event.Reference, "khipu:") {
		return false
	}
	receiptID := paymentOperationID(event.Reference)
	r, err := a.Accounts.read(ctx, "RECEIPT#"+receiptID, "META")
	return err == nil && r.Event != nil && r.Event.Type == "PAYMENT_RECEIVED" && validReceiptDocument(r, accountID, receiptID, attemptID)
}

// HandleAttempt consulta solo intentos pertenecientes a la cuenta autorizada.
func (a PortalApp) HandleAttempt(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if req.RequestContext.HTTP.Method != "GET" {
		return portalFailure(405)
	}
	if a.Now == nil {
		return portalFailure(503)
	}
	id := req.PathParameters["id"]
	if !validPortalID(id) {
		return portalFailure(404)
	}
	account, err := a.Accounts.PassengerAccount(ctx, req)
	if err != nil {
		return portalFailure(403)
	}
	view, err := a.attemptView(ctx, account, id)
	if errors.Is(err, ErrNotFound) {
		return portalFailure(404)
	}
	if err != nil {
		return portalFailure(503)
	}
	return portalResponse(200, view)
}
