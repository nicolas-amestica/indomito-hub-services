package collection

import (
	"context"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"strings"
	"sync"
	"time"
)

type TreasuryAdminApp struct {
	Accounts Service
	Now      func() time.Time
}
type SettlementRequest struct {
	CommandID     string `json:"commandId"`
	Version       int64  `json:"version"`
	Gross         int64  `json:"gross"`
	Fee           int64  `json:"fee"`
	BankReference string `json:"bankReference"`
	EffectiveDate string `json:"effectiveDate"`
	Reason        string `json:"reason"`
}
type CashView struct {
	Period     domain.CashPeriod `json:"period"`
	InTransit  int64             `json:"inTransit"`
	ActualFees int64             `json:"actualFees"`
	Position   domain.Position   `json:"position"`
	Suppliers  SupplierExposure  `json:"suppliers"`
	Events     []domain.Event    `json:"events"`
}

type SupplierExposure struct {
	CommittedPending int64 `json:"committedPending"`
	RefundExpected   int64 `json:"refundExpected"`
}

func (a TreasuryAdminApp) HandleSettlements(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	tripID := req.PathParameters["tripId"]
	paymentID := req.PathParameters["paymentId"]
	if !validPortalID(tripID) || a.Now == nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if req.RequestContext.HTTP.Method == "GET" {
		page, err := a.Accounts.ListSettlements(ctx, tripID, req.QueryStringParameters["cursor"])
		if err != nil {
			return domainFailure(req, err)
		}
		return lambdautil.SuccessResponseWithHeaders(200, page, map[string]string{"cache-control": "no-store"})
	}
	if req.RequestContext.HTTP.Method != "POST" || !checkoutPaymentID.MatchString(paymentID) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	var body SettlementRequest
	if decodeAdmin(req, &body) != nil || !validPortalID(body.CommandID) || body.Version < 1 || body.Gross <= 0 || body.Fee < 0 || body.Fee > body.Gross || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 || len(body.BankReference) < 5 || len(body.BankReference) > 150 || strings.TrimSpace(body.BankReference) != body.BankReference {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	date, err := time.Parse(time.DateOnly, body.EffectiveDate)
	zone, zoneErr := time.LoadLocation("America/Santiago")
	if err != nil || zoneErr != nil || date.Format(time.DateOnly) > a.Now().In(zone).Format(time.DateOnly) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	commandID := paymentOperationID("settlement:" + tripID + ":" + paymentID + ":" + body.CommandID)
	settlement, event, err := a.Accounts.ReconcileSettlement(ctx, tripID, "khipu:"+paymentID, body.Version, body.Gross, body.Fee, "bank:"+body.BankReference, body.EffectiveDate, domain.Audit{CommandID: commandID, Actor: actor, Reason: strings.TrimSpace(body.Reason), RecordedAt: a.Now().UTC()})
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, map[string]any{"settlement": settlement, "event": event}, map[string]string{"cache-control": "no-store"})
}

func (a TreasuryAdminApp) HandleCash(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	tripID := req.PathParameters["tripId"]
	from, to := req.QueryStringParameters["from"], req.QueryStringParameters["to"]
	if req.RequestContext.HTTP.Method != "GET" || !validPortalID(tripID) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	fromDate, e1 := time.Parse(time.DateOnly, from)
	_, e2 := time.Parse(time.DateOnly, to)
	if e1 != nil || e2 != nil || from > to {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	db, ok := a.Accounts.DB.(queryDatabase)
	if !ok {
		return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
	}
	eventsList := []domain.Event{}
	var cursor map[string]types.AttributeValue
	for {
		out, err := db.Query(ctx, &dynamodb.QueryInput{TableName: &a.Accounts.Table, KeyConditionExpression: aws.String("pk = :pk AND sk BETWEEN :from AND :to"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "TRIP#" + tripID}, ":from": &types.AttributeValueMemberS{Value: "CASH#0000"}, ":to": &types.AttributeValueMemberS{Value: "CASH#" + to + "~"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(50), ExclusiveStartKey: cursor})
		if err != nil {
			return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
		}
		for _, raw := range out.Items {
			var row record
			if attributevalue.UnmarshalMap(raw, &row) != nil || row.Event == nil {
				return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
			}
			eventsList = append(eventsList, *row.Event)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		cursor = out.LastEvaluatedKey
	}
	openingPeriod, err := domain.ProjectCash(0, "1900-01-01", fromDate.AddDate(0, 0, -1).Format(time.DateOnly), tripID, eventsList)
	if err != nil {
		return domainFailure(req, err)
	}
	period, err := domain.ProjectCash(openingPeriod.Closing, from, to, tripID, eventsList)
	if err != nil {
		return domainFailure(req, err)
	}
	view := CashView{Period: period, Events: []domain.Event{}}
	settlementCursor := ""
	for {
		settlements, listErr := a.Accounts.ListSettlements(ctx, tripID, settlementCursor)
		if listErr != nil {
			return domainFailure(req, listErr)
		}
		for _, settlement := range settlements.Items {
			view.InTransit += settlement.Amount - settlement.SettledGross
			view.ActualFees += settlement.ActualFees
		}
		if settlements.NextCursor == "" {
			break
		}
		settlementCursor = settlements.NextCursor
	}
	for _, e := range eventsList {
		if e.EffectiveDate >= from && e.EffectiveDate <= to {
			view.Events = append(view.Events, e)
		}
	}
	position, exposure, err := a.tripFinancialPosition(ctx, tripID)
	if err != nil {
		return domainFailure(req, err)
	}
	view.Position, view.Suppliers = position, exposure
	return lambdautil.SuccessResponseWithHeaders(200, view, map[string]string{"cache-control": "no-store"})
}

func (a TreasuryAdminApp) tripFinancialPosition(ctx context.Context, tripID string) (domain.Position, SupplierExposure, error) {
	accountIDs := []string{}
	cursor := ""
	for {
		page, err := a.Accounts.ListRoster(ctx, tripID, cursor)
		if err != nil {
			return domain.Position{}, SupplierExposure{}, err
		}
		for _, member := range page.Items {
			accountIDs = append(accountIDs, member.AccountID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	type accountResult struct {
		position domain.Position
		err      error
	}
	jobs, results := make(chan string), make(chan accountResult)
	workers := 10
	if len(accountIDs) < workers {
		workers = len(accountIDs)
	}
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for id := range jobs {
				account, err := a.Accounts.GetAccount(ctx, id)
				if err != nil || account.TripID != tripID {
					results <- accountResult{err: domain.ErrConflict}
					continue
				}
				results <- accountResult{position: account.Position()}
			}
		}()
	}
	go func() {
		for _, id := range accountIDs {
			jobs <- id
		}
		close(jobs)
		group.Wait()
		close(results)
	}()
	position := domain.Position{}
	for result := range results {
		if result.err != nil {
			return domain.Position{}, SupplierExposure{}, result.err
		}
		position.Receivable += result.position.Receivable
		position.DepositReceivable += result.position.DepositReceivable
		position.InstallmentReceivable += result.position.InstallmentReceivable
		position.AppliedReceipts += result.position.AppliedReceipts
		position.UnappliedReceipts += result.position.UnappliedReceipts
		position.Discounts += result.position.Discounts
		position.Cancelled += result.position.Cancelled
		position.RefundPayable += result.position.RefundPayable
		position.Refunded += result.position.Refunded
	}
	exposure := SupplierExposure{}
	cursor = ""
	for {
		page, err := a.Accounts.ListSuppliers(ctx, tripID, cursor)
		if err != nil {
			return domain.Position{}, SupplierExposure{}, err
		}
		for _, supplier := range page.Items {
			remaining := supplier.Committed - supplier.Paid + supplier.RefundReceived
			if remaining > 0 {
				exposure.CommittedPending += remaining
			}
			exposure.RefundExpected += supplier.RefundAgreed - supplier.RefundReceived
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return position, exposure, nil
}

func TreasurySettlementsHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (TreasuryAdminApp{Accounts: app.Accounts, Now: time.Now}).HandleSettlements(ctx, req)
}
func TreasuryCashHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (TreasuryAdminApp{Accounts: app.Accounts, Now: time.Now}).HandleCash(ctx, req)
}

func ConsolidatedCashHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	if req.RequestContext.HTTP.Method != "GET" {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	view, err := app.Accounts.ConsolidatedCash(ctx, req.QueryStringParameters["from"], req.QueryStringParameters["to"])
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, view, map[string]string{"cache-control": "no-store"})
}
