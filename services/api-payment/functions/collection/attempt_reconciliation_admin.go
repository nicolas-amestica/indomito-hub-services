package collection

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type AttemptReconciliationApp struct {
	Accounts  Service
	Inspector PaymentInspector
	Verifier  PaymentVerifier
	Zone      *time.Location
	Now       func() time.Time
}
type AttemptReconciliationRequest struct {
	CommandID string `json:"commandId"`
	PaymentID string `json:"paymentId"`
	Reason    string `json:"reason"`
}

func (a AttemptReconciliationApp) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	attemptID := req.PathParameters["attemptId"]
	if req.RequestContext.HTTP.Method != "POST" || !validPortalID(attemptID) || a.Now == nil || a.Zone == nil || a.Inspector == nil || a.Verifier == nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	var body AttemptReconciliationRequest
	if decodeAdmin(req, &body) != nil || !validPortalID(body.CommandID) || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 || (body.PaymentID != "" && !checkoutPaymentID.MatchString(body.PaymentID)) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	commandID := paymentOperationID("attempt-reconciliation:" + attemptID + ":" + body.CommandID)
	recordedAt := a.Now().UTC()
	state, err := a.Accounts.ReconcileProviderAttempt(ctx, a.Inspector, a.Verifier, attemptID, body.PaymentID, domain.Audit{CommandID: commandID, Actor: actor, Reason: strings.TrimSpace(body.Reason), RecordedAt: recordedAt}, a.Now(), a.Zone)
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, state, map[string]string{"cache-control": "no-store"})
}

func AttemptReconciliationHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	webhook, err := getWebhookApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	inspector, ok := webhook.Verifier.(PaymentInspector)
	if !ok {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (AttemptReconciliationApp{Accounts: webhook.Accounts, Inspector: inspector, Verifier: webhook.Verifier, Zone: webhook.Zone, Now: time.Now}).Handle(ctx, req)
}
