package collection

import (
	"context"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
)

type OperationRecovery struct {
	Status     string `json:"status"`
	Kind       string `json:"kind"`
	CommandID  string `json:"commandId"`
	Event      any    `json:"event"`
	Account    any    `json:"account,omitempty"`
	Supplier   any    `json:"supplier,omitempty"`
	Settlement any    `json:"settlement,omitempty"`
}

func (a TreasuryAdminApp) HandleOperationRecovery(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	clientID := req.PathParameters["id"]
	kind, tripID, entityID := strings.ToUpper(req.QueryStringParameters["kind"]), req.QueryStringParameters["tripId"], req.QueryStringParameters["entityId"]
	if req.RequestContext.HTTP.Method != "GET" || !validPortalID(clientID) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	var internalID string
	switch kind {
	case "ACCOUNT":
		if !validPortalID(entityID) {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		internalID = paymentOperationID("admin:" + entityID + ":" + clientID)
	case "SUPPLIER":
		if !validPortalID(tripID) || !validPortalID(entityID) {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		internalID = paymentOperationID("supplier:" + tripID + ":" + entityID + ":" + clientID)
	case "SETTLEMENT":
		if !validPortalID(tripID) || !checkoutPaymentID.MatchString(entityID) {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		internalID = paymentOperationID("settlement:" + tripID + ":" + entityID + ":" + clientID)
	default:
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	command, err := a.Accounts.read(ctx, "COMMAND#"+internalID, "META")
	if err != nil {
		return domainFailure(req, err)
	}
	result := OperationRecovery{Status: "APPLIED", Kind: kind, CommandID: clientID, Event: command.Event}
	switch kind {
	case "ACCOUNT":
		if command.Change == nil {
			return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
		}
		result.Event = command.Change.Event
		result.Account = command.Change.Account
	case "SUPPLIER":
		if command.Supplier == nil || command.Event == nil {
			return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
		}
		result.Supplier = command.Supplier
	case "SETTLEMENT":
		row, e := a.Accounts.read(ctx, "TRIP#"+tripID, "SETTLEMENT#khipu:"+entityID)
		if e != nil {
			return domainFailure(req, e)
		}
		if row.Settlement == nil {
			return domainFailure(req, ErrNotFound)
		}
		result.Settlement = row.Settlement
	}
	return lambdautil.SuccessResponseWithHeaders(200, result, map[string]string{"cache-control": "no-store"})
}

func OperationRecoveryHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (TreasuryAdminApp{Accounts: app.Accounts}).HandleOperationRecovery(ctx, req)
}
