package collection

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type RosterMigrationApp struct {
	Accounts  Service
	Contracts ContractSource
	Now       func() time.Time
}

type RosterMigrationRequest struct {
	CommandID string `json:"commandId"`
	Reason    string `json:"reason"`
}

// Handle reconstruye la proyección desde fuentes internas ya persistidas. El
// navegador solo aporta idempotencia y motivo, nunca integrantes ni versiones.
func (a RosterMigrationApp) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
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
	view, err := a.Contracts.LoadApproved(ctx, tripID)
	if err != nil {
		return domainFailure(req, err)
	}
	plan, err := a.Accounts.read(ctx, "TRIP#"+tripID, "META")
	if err != nil {
		return domainFailure(req, err)
	}
	snapshot, err := a.Accounts.read(ctx, plan.PK, "SETUP")
	if err != nil || snapshot.Startup == nil || snapshot.Startup.ContractVersion != view.ContractVersion || len(snapshot.Startup.Participants) != len(view.Members) {
		return domainFailure(req, domain.ErrConflict)
	}
	participants := map[string]bool{}
	for _, participant := range snapshot.Startup.Participants {
		participants[participant.ID] = true
	}
	profiles := make([]RosterProjection, 0, len(view.Members))
	for _, member := range view.Members {
		if !participants[member.ID] {
			return domainFailure(req, domain.ErrConflict)
		}
		delete(participants, member.ID)
		profiles = append(profiles, RosterProjection{AccountID: member.ID, ParticipantID: member.ID, Name: member.Name, Document: member.DNI})
	}
	if len(participants) != 0 {
		return domainFailure(req, domain.ErrConflict)
	}
	reason := strings.TrimSpace(body.Reason)
	commandID := paymentOperationID("roster-migration:" + tripID + ":" + body.CommandID)
	recordedAt := a.Now().UTC()
	if root, readErr := a.Accounts.read(ctx, plan.PK, rosterMigrationSK); readErr == nil && root.Event != nil && root.Event.CommandID == commandID && root.Event.Actor == actor && root.Event.Reason == reason {
		recordedAt = root.Event.RecordedAt
	}
	state, err := a.Accounts.BackfillRoster(ctx, tripID, profiles, domain.Audit{CommandID: commandID, Actor: actor, Reason: reason, RecordedAt: recordedAt})
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, state, map[string]string{"cache-control": "no-store"})
}
