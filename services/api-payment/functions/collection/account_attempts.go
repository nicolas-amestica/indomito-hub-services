package collection

import (
	"context"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
)

type AccountAttemptSummary struct {
	ID            string `json:"id"`
	InstallmentID string `json:"installmentId"`
	Amount        int64  `json:"amount"`
	Status        string `json:"status"`
}
type AccountAttemptPage struct {
	Items      []AccountAttemptSummary `json:"items"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}
type AccountAttemptsApp struct{ Accounts Service }

func (a AccountAttemptsApp) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	id := req.PathParameters["accountId"]
	cursor := req.QueryStringParameters["cursor"]
	if req.RequestContext.HTTP.Method != "GET" || !validPortalID(id) || (cursor != "" && !validPortalID(cursor)) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if _, err := a.Accounts.GetAccount(ctx, id); err != nil {
		return domainFailure(req, err)
	}
	db, ok := a.Accounts.DB.(queryDatabase)
	if !ok {
		return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
	}
	input := &dynamodb.QueryInput{TableName: &a.Accounts.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "ACCOUNT#" + id}, ":prefix": &types.AttributeValueMemberS{Value: "ATTEMPT#"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(20)}
	if cursor != "" {
		input.ExclusiveStartKey = key("ACCOUNT#"+id, "ATTEMPT#"+cursor)
	}
	out, err := db.Query(ctx, input)
	if err != nil {
		return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
	}
	page := AccountAttemptPage{Items: []AccountAttemptSummary{}}
	for _, raw := range out.Items {
		var row record
		if attributevalue.UnmarshalMap(raw, &row) != nil || row.Attempt == nil || row.AccountID != id {
			return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
		}
		status := "RECONCILIATION_REQUIRED"
		if resolution, e := a.Accounts.read(ctx, "ATTEMPT#"+row.Attempt.ID, "RESOLUTION"); e == nil && resolution.Event != nil {
			if resolution.Event.Type == "PAYMENT_REVERSED_BY_PROVIDER" {
				status = "REVERSED"
			} else {
				status = "UNPAID_FINAL"
			}
		} else if outcome, e := a.Accounts.read(ctx, "ATTEMPT#"+row.Attempt.ID, "OUTCOME"); e == nil && outcome.Event != nil {
			if outcome.Event.Type == "PAYMENT_RECEIVED" {
				status = "CONFIRMED"
			} else {
				status = "REVIEW_REQUIRED"
			}
		} else if checkout, e := a.Accounts.read(ctx, "ATTEMPT#"+row.Attempt.ID, "CHECKOUT"); e == nil && checkout.Checkout != nil {
			status = "PENDING_OR_EXPIRED"
		}
		page.Items = append(page.Items, AccountAttemptSummary{ID: row.Attempt.ID, InstallmentID: row.Attempt.InstallmentID, Amount: row.Attempt.Amount, Status: status})
	}
	if sk, ok := out.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
		page.NextCursor = strings.TrimPrefix(sk.Value, "ATTEMPT#")
	}
	return lambdautil.SuccessResponseWithHeaders(200, page, map[string]string{"cache-control": "no-store"})
}

func AccountAttemptsHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (AccountAttemptsApp{Accounts: app.Accounts}).Handle(ctx, req)
}
