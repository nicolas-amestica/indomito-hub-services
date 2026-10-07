package collection

import (
	"context"
	"net/mail"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type GroupDepositApp struct {
	Accounts Service
	Now      func() time.Time
}

type GroupDepositRequest struct {
	CommandID     string `json:"commandId"`
	Amount        int64  `json:"amount"`
	Reference     string `json:"reference"`
	EffectiveDate string `json:"effectiveDate"`
	Email         string `json:"email"`
	Reason        string `json:"reason"`
}

func (a GroupDepositApp) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	tripID := req.PathParameters["tripId"]
	if !validPortalID(tripID) || a.Now == nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if req.RequestContext.HTTP.Method == "GET" {
		commandID := paymentOperationID("group-deposit:" + tripID + ":" + req.PathParameters["commandId"])
		if !validPortalID(req.PathParameters["commandId"]) {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		root, err := a.Accounts.read(ctx, "TRIP#"+tripID, "GROUP_DEPOSIT#"+commandID)
		if err != nil {
			return domainFailure(req, err)
		}
		return lambdautil.SuccessResponseWithHeaders(200, groupDepositState(root), map[string]string{"cache-control": "no-store"})
	}
	if req.RequestContext.HTTP.Method != "POST" {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	var body GroupDepositRequest
	if decodeAdmin(req, &body) != nil || !validPortalID(body.CommandID) || body.Amount <= 0 || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 || len(body.Reference) < 5 || len(body.Reference) > 150 || strings.TrimSpace(body.Reference) != body.Reference {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	address, emailErr := mail.ParseAddress(body.Email)
	date, dateErr := time.Parse(time.DateOnly, body.EffectiveDate)
	zone, zoneErr := time.LoadLocation("America/Santiago")
	if emailErr != nil || address.Address != body.Email || len(body.Email) > 254 || dateErr != nil || zoneErr != nil || date.Format(time.DateOnly) > a.Now().In(zone).Format(time.DateOnly) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	commandID := paymentOperationID("group-deposit:" + tripID + ":" + body.CommandID)
	reference := "bank:" + body.Reference
	recordedAt := a.Now().UTC()
	if root, err := a.Accounts.read(ctx, "TRIP#"+tripID, "GROUP_DEPOSIT#"+commandID); err == nil && root.Event != nil && root.Event.CommandID == commandID && root.Event.Actor == actor && root.Event.Reason == strings.TrimSpace(body.Reason) {
		recordedAt = root.Event.RecordedAt
	}
	state, err := a.Accounts.ApplyGroupDeposit(ctx, GroupDepositInput{ID: commandID, TripID: tripID, Amount: body.Amount, Reference: reference, EffectiveDate: body.EffectiveDate, ReceiptEmail: body.Email, Audit: domain.Audit{CommandID: commandID, Actor: actor, Reason: strings.TrimSpace(body.Reason), RecordedAt: recordedAt}})
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, state, map[string]string{"cache-control": "no-store"})
}
