package collection

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type GroupDiscountApp struct {
	Accounts Service
	Now      func() time.Time
}
type GroupDiscountRequest struct {
	ID          string   `json:"id"`
	AccountIDs  []string `json:"accountIds"`
	BasisPoints int64    `json:"basisPoints"`
	Reason      string   `json:"reason"`
}
type GroupDiscountApprovalRequest struct {
	CommandID string `json:"commandId"`
	Reason    string `json:"reason"`
}

func (a GroupDiscountApp) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	tripID := req.PathParameters["tripId"]
	publicID := req.PathParameters["discountId"]
	if !validPortalID(tripID) || a.Now == nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if req.RequestContext.HTTP.Method == "GET" {
		if !validPortalID(publicID) {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		id := paymentOperationID("group-discount:" + tripID + ":" + publicID)
		root, err := a.Accounts.read(ctx, "TRIP#"+tripID, "GROUP_DISCOUNT#"+id)
		if err != nil {
			return domainFailure(req, err)
		}
		return lambdautil.SuccessResponseWithHeaders(200, groupDiscountState(root), map[string]string{"cache-control": "no-store"})
	}
	if publicID == "" {
		if req.RequestContext.HTTP.Method != "POST" {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		var body GroupDiscountRequest
		if decodeAdmin(req, &body) != nil || !validPortalID(body.ID) || body.BasisPoints <= 0 || body.BasisPoints > 10000 || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 || len(body.AccountIDs) > 500 {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		id := paymentOperationID("group-discount:" + tripID + ":" + body.ID)
		recordedAt := a.Now().UTC()
		if root, err := a.Accounts.read(ctx, "TRIP#"+tripID, "GROUP_DISCOUNT#"+id); err == nil && root.Event != nil && root.Event.Actor == actor && root.Event.Reason == strings.TrimSpace(body.Reason) {
			recordedAt = root.Event.RecordedAt
		}
		state, err := a.Accounts.CreateGroupDiscount(ctx, GroupDiscountInput{ID: id, TripID: tripID, AccountIDs: body.AccountIDs, BasisPoints: body.BasisPoints, CreationAudit: domain.Audit{CommandID: id, Actor: actor, Reason: strings.TrimSpace(body.Reason), RecordedAt: recordedAt}})
		if err != nil {
			return domainFailure(req, err)
		}
		return lambdautil.SuccessResponseWithHeaders(200, state, map[string]string{"cache-control": "no-store"})
	}
	if req.RequestContext.HTTP.Method != "POST" {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	var body GroupDiscountApprovalRequest
	if decodeAdmin(req, &body) != nil || !validPortalID(publicID) || !validPortalID(body.CommandID) || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	id := paymentOperationID("group-discount:" + tripID + ":" + publicID)
	commandID := paymentOperationID("group-discount-approval:" + tripID + ":" + publicID + ":" + body.CommandID)
	recordedAt := a.Now().UTC()
	if root, err := a.Accounts.read(ctx, "TRIP#"+tripID, "GROUP_DISCOUNT#"+id); err == nil && root.Approval != nil && root.Approval.CommandID == commandID && root.Approval.Actor == actor && root.Approval.Reason == strings.TrimSpace(body.Reason) {
		recordedAt = root.Approval.RecordedAt
	}
	state, err := a.Accounts.ApproveGroupDiscount(ctx, tripID, id, domain.Audit{CommandID: commandID, Actor: actor, Reason: strings.TrimSpace(body.Reason), RecordedAt: recordedAt})
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, state, map[string]string{"cache-control": "no-store"})
}
