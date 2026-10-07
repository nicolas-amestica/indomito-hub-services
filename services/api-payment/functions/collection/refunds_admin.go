package collection

import (
	"context"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type RefundRow struct {
	AccountID                string `json:"accountId"`
	Name                     string `json:"name"`
	Document                 string `json:"document"`
	Active                   bool   `json:"active"`
	PaidInstallments         int64  `json:"paidInstallments"`
	WithdrawalRefundApproved int64  `json:"withdrawalRefundApproved"`
	UnappliedReceived        int64  `json:"unappliedReceived"`
	UnappliedRefundApproved  int64  `json:"unappliedRefundApproved"`
	Refunded                 int64  `json:"refunded"`
	RefundPayable            int64  `json:"refundPayable"`
	Version                  int64  `json:"version"`
}

type RefundPage struct {
	Items      []RefundRow `json:"items"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

func (a TreasuryAdminApp) HandleRefunds(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	tripID := req.PathParameters["tripId"]
	if req.RequestContext.HTTP.Method != "GET" || !validPortalID(tripID) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	roster, err := a.Accounts.ListRoster(ctx, tripID, req.QueryStringParameters["cursor"])
	if err != nil {
		return domainFailure(req, err)
	}
	page := RefundPage{Items: []RefundRow{}, NextCursor: roster.NextCursor}
	for _, member := range roster.Items {
		account, readErr := a.Accounts.GetAccount(ctx, member.AccountID)
		if readErr != nil {
			return domainFailure(req, readErr)
		}
		if account.TripID != tripID {
			return domainFailure(req, domain.ErrConflict)
		}
		paid := int64(0)
		for _, installment := range account.Installments {
			paid += installment.Paid
		}
		position := account.Position()
		if account.Active && account.UnappliedReceived == 0 && position.RefundPayable == 0 {
			continue
		}
		page.Items = append(page.Items, RefundRow{AccountID: account.ID, Name: member.Name, Document: member.Document, Active: account.Active, PaidInstallments: paid, WithdrawalRefundApproved: account.WithdrawalRefundApproved, UnappliedReceived: account.UnappliedReceived, UnappliedRefundApproved: account.UnappliedRefundApproved, Refunded: account.Refunded, RefundPayable: position.RefundPayable, Version: account.Version})
	}
	return lambdautil.SuccessResponseWithHeaders(200, page, map[string]string{"cache-control": "no-store"})
}

func TreasuryRefundsHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (TreasuryAdminApp{Accounts: app.Accounts}).HandleRefunds(ctx, req)
}
