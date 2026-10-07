package collection

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type RosterClosureApp struct {
	Accounts Service
	Now      func() time.Time
}

// Handle cierra únicamente la edición de nómina; no cambia el estado ACTIVE
// usado por pagos y devoluciones.
func (a RosterClosureApp) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	tripID := req.PathParameters["tripId"]
	if req.RequestContext.HTTP.Method != "POST" || !validPortalID(tripID) || a.Now == nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	var body RosterMigrationRequest
	if decodeAdmin(req, &body) != nil || !validPortalID(body.CommandID) || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	reason := strings.TrimSpace(body.Reason)
	commandID := paymentOperationID("roster-closure:" + tripID + ":" + body.CommandID)
	recordedAt := a.Now().UTC()
	if plan, err := a.Accounts.read(ctx, "TRIP#"+tripID, "META"); err == nil && plan.RosterClosure != nil && plan.RosterClosure.CommandID == commandID && plan.RosterClosure.Actor == actor && plan.RosterClosure.Reason == reason {
		recordedAt = plan.RosterClosure.RecordedAt
	}
	if err := a.Accounts.CloseRoster(ctx, tripID, domain.Audit{CommandID: commandID, Actor: actor, Reason: reason, RecordedAt: recordedAt}); err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, map[string]any{"tripId": tripID, "closed": true}, map[string]string{"cache-control": "no-store"})
}
