package collection

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/paymentaccess"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type TripAccessView struct {
	TripID    string    `json:"tripId"`
	TripCode  string    `json:"tripCode,omitempty"`
	Version   int64     `json:"version"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}
type TripAccessRequest struct {
	CommandID string `json:"commandId"`
	Reason    string `json:"reason"`
	Version   int64  `json:"version"`
}

func newPaymentTripCode(reader io.Reader) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, 6)
	for i := range out {
		var b [1]byte
		if _, err := io.ReadFull(reader, b[:]); err != nil {
			return "", err
		}
		out[i] = alphabet[int(b[0])%len(alphabet)]
	}
	return string(out), nil
}

func (a AdminApp) HandleTripAccess(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	tripID := req.PathParameters["tripId"]
	if !validPortalID(tripID) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if req.RequestContext.HTTP.Method == "GET" {
		view, err := a.tripAccess(ctx, tripID)
		if err != nil {
			return domainFailure(req, err)
		}
		return lambdautil.SuccessResponseWithHeaders(200, view, map[string]string{"cache-control": "no-store"})
	}
	if req.RequestContext.HTTP.Method != "POST" || len(a.LookupSecret) < 32 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	var body TripAccessRequest
	if decodeAdmin(req, &body) != nil || !validPortalID(body.CommandID) || body.Version < 1 || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	audit := domain.Audit{CommandID: paymentOperationID("access:" + tripID + ":" + body.CommandID), Actor: actor, Reason: strings.TrimSpace(body.Reason), RecordedAt: time.Now().UTC()}
	view, err := a.changeTripAccess(ctx, tripID, strings.ToUpper(req.PathParameters["action"]), body.Version, audit)
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, view, map[string]string{"cache-control": "no-store"})
}

func (a AdminApp) tripAccess(ctx context.Context, tripID string) (TripAccessView, error) {
	if row, err := a.Accounts.read(ctx, "TRIP#"+tripID, "ACCESS"); err == nil {
		view := TripAccessView{TripID: tripID, TripCode: row.TripCode, Version: row.CodeVersion, Status: row.Status}
		if row.AccessAudit != nil {
			view.UpdatedAt = row.AccessAudit.RecordedAt
		}
		return view, nil
	} else if !errors.Is(err, ErrNotFound) {
		return TripAccessView{}, err
	}
	approval, err := a.Accounts.read(ctx, "TRIP#"+tripID, "APPROVAL")
	if err != nil || approval.CodeKey == "" {
		return TripAccessView{}, ErrNotFound
	}
	tripCode := approval.TripCode
	if tripCode == "" {
		contract, contractErr := a.Contracts.LoadApproved(ctx, tripID)
		if contractErr != nil || len(contract.TripCode) != 6 {
			return TripAccessView{}, ErrNotFound
		}
		tripCode = contract.TripCode
	}
	return TripAccessView{TripID: tripID, TripCode: tripCode, Version: 1, Status: "ACTIVE"}, nil
}

func (a AdminApp) changeTripAccess(ctx context.Context, tripID, action string, version int64, audit domain.Audit) (TripAccessView, error) {
	if prior, err := a.Accounts.read(ctx, "COMMAND#"+audit.CommandID, "META"); err == nil {
		if prior.TripID != tripID || prior.CodeVersion != version+1 || prior.AccessAudit == nil {
			return TripAccessView{}, ErrReplayMismatch
		}
		return TripAccessView{TripID: tripID, TripCode: prior.TripCode, Version: prior.CodeVersion, Status: prior.Status, UpdatedAt: prior.AccessAudit.RecordedAt}, nil
	}
	current, err := a.Accounts.read(ctx, "TRIP#"+tripID, "ACCESS")
	first := false
	if errors.Is(err, ErrNotFound) {
		current, err = a.Accounts.read(ctx, "TRIP#"+tripID, "APPROVAL")
		first = true
	}
	if err != nil || current.CodeKey == "" {
		return TripAccessView{}, ErrNotFound
	}
	currentVersion := current.CodeVersion
	if currentVersion == 0 {
		currentVersion = 1
	}
	if currentVersion != version || current.Status == "REVOKED" {
		return TripAccessView{}, domain.ErrConflict
	}
	next := record{PK: "TRIP#" + tripID, SK: "ACCESS", TripID: tripID, Version: version + 1, CodeVersion: version + 1, AccessAudit: &audit}
	if action == "ROTATE" {
		code, codeErr := newPaymentTripCode(rand.Reader)
		if codeErr != nil {
			return TripAccessView{}, codeErr
		}
		codeKey, keyErr := paymentaccess.CodeKey(a.LookupSecret, code)
		if keyErr != nil {
			return TripAccessView{}, keyErr
		}
		next.TripCode, next.CodeKey, next.Status = code, codeKey, "ACTIVE"
	} else if action == "REVOKE" {
		next.Status = "REVOKED"
	} else {
		return TripAccessView{}, domain.ErrInvalid
	}
	oldWrite, err := a.Accounts.put(record{PK: current.CodeKey, SK: "META", TripID: tripID, CodeVersion: version, Status: "REVOKED"}, "#status = :active", map[string]types.AttributeValue{":active": &types.AttributeValueMemberS{Value: "RESERVED_APPROVED"}})
	if err != nil {
		return TripAccessView{}, err
	}
	condition := "attribute_not_exists(pk)"
	var values map[string]types.AttributeValue
	if !first {
		condition, values = "#version = :previous", versionValue(version)
	}
	accessWrite, err := a.Accounts.put(next, condition, values)
	if err != nil {
		return TripAccessView{}, err
	}
	command := record{PK: "COMMAND#" + audit.CommandID, SK: "META", TripID: tripID, TripCode: next.TripCode, CodeKey: next.CodeKey, CodeVersion: next.CodeVersion, Status: next.Status, AccessAudit: &audit}
	commandWrite, err := a.Accounts.put(command, "attribute_not_exists(pk)", nil)
	if err != nil {
		return TripAccessView{}, err
	}
	writes := []types.TransactWriteItem{oldWrite, accessWrite, commandWrite}
	if action == "ROTATE" {
		newWrite, buildErr := a.Accounts.put(record{PK: next.CodeKey, SK: "META", TripID: tripID, CodeVersion: next.CodeVersion, Status: "RESERVED_APPROVED"}, "attribute_not_exists(pk)", nil)
		if buildErr != nil {
			return TripAccessView{}, buildErr
		}
		writes = append(writes, newWrite)
	}
	_, err = a.Accounts.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err != nil {
		if saved, readErr := a.Accounts.read(ctx, "COMMAND#"+audit.CommandID, "META"); readErr == nil && saved.AccessAudit != nil {
			return TripAccessView{TripID: tripID, TripCode: saved.TripCode, Version: saved.CodeVersion, Status: saved.Status, UpdatedAt: saved.AccessAudit.RecordedAt}, nil
		}
		return TripAccessView{}, domain.ErrConflict
	}
	return TripAccessView{TripID: tripID, TripCode: next.TripCode, Version: next.CodeVersion, Status: next.Status, UpdatedAt: audit.RecordedAt}, nil
}

func TripAccessHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var app *AdminApp
	var err error
	if req.RequestContext.HTTP.Method == "GET" {
		app, err = getAdminApp(ctx)
	} else {
		app, err = getAdminAppWithLookup(ctx)
	}
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return app.HandleTripAccess(ctx, req)
}
