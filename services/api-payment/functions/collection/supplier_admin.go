package collection

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type SupplierRequest struct {
	CommandID     string `json:"commandId"`
	Version       int64  `json:"version"`
	Operation     string `json:"operation"`
	Name          string `json:"name"`
	Service       string `json:"service"`
	Committed     int64  `json:"committed"`
	RefundAgreed  int64  `json:"refundAgreed"`
	Amount        int64  `json:"amount"`
	Reference     string `json:"reference"`
	EffectiveDate string `json:"effectiveDate"`
	AnnexID       string `json:"annexId"`
	Reason        string `json:"reason"`
}

func (a TreasuryAdminApp) HandleSuppliers(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	tripID, supplierID := req.PathParameters["tripId"], req.PathParameters["supplierId"]
	if !validPortalID(tripID) || a.Now == nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if req.RequestContext.HTTP.Method == "GET" {
		page, err := a.Accounts.ListSuppliers(ctx, tripID, req.QueryStringParameters["cursor"])
		if err != nil {
			return domainFailure(req, err)
		}
		return lambdautil.SuccessResponseWithHeaders(200, page, map[string]string{"cache-control": "no-store"})
	}
	if req.RequestContext.HTTP.Method != "POST" || !validPortalID(supplierID) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	var body SupplierRequest
	if decodeAdmin(req, &body) != nil || !validPortalID(body.CommandID) || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	operation := SupplierOperation{Type: body.Operation, Name: strings.TrimSpace(body.Name), Service: strings.TrimSpace(body.Service), Committed: body.Committed, RefundAgreed: body.RefundAgreed, Amount: body.Amount, AnnexID: strings.TrimSpace(body.AnnexID)}
	switch body.Operation {
	case "CREATE":
		if body.Version != 0 || operation.Name != body.Name || operation.Service != body.Service || len(body.Name) < 2 || len(body.Name) > 150 || len(body.Service) < 2 || len(body.Service) > 250 || body.Committed <= 0 {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
	case "REVISE":
		if body.Version < 1 || body.Committed < 0 || body.RefundAgreed < 0 || len(operation.AnnexID) < 5 || len(operation.AnnexID) > 150 {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
	case "PAY", "RECEIVE_REFUND":
		date, dateErr := time.Parse(time.DateOnly, body.EffectiveDate)
		zone, zoneErr := time.LoadLocation("America/Santiago")
		if body.Version < 1 || body.Amount <= 0 || dateErr != nil || zoneErr != nil || date.Format(time.DateOnly) > a.Now().In(zone).Format(time.DateOnly) || len(body.Reference) < 5 || len(body.Reference) > 150 || strings.TrimSpace(body.Reference) != body.Reference {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		operation.Reference = "bank:" + body.Reference
		operation.EffectiveDate = body.EffectiveDate
	default:
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	commandID := paymentOperationID("supplier:" + tripID + ":" + supplierID + ":" + body.CommandID)
	supplier, event, err := a.Accounts.ApplySupplierOperation(ctx, tripID, supplierID, body.Version, operation, domain.Audit{CommandID: commandID, Actor: actor, Reason: strings.TrimSpace(body.Reason), RecordedAt: a.Now().UTC()})
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, map[string]any{"supplier": supplier, "event": event}, map[string]string{"cache-control": "no-store"})
}

func TreasurySuppliersHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (TreasuryAdminApp{Accounts: app.Accounts, Now: time.Now}).HandleSuppliers(ctx, req)
}
