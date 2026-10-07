package collection

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type ReceiptBackfillRequest struct {
	CommandID string `json:"commandId"`
	Cursor    string `json:"cursor,omitempty"`
	Reason    string `json:"reason"`
	Apply     bool   `json:"apply"`
}
type ReceiptBackfillResult struct {
	Apply      bool   `json:"apply"`
	Eligible   int    `json:"eligible"`
	Enqueued   int    `json:"enqueued"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func (a AdminApp) HandleReceiptBackfill(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	accountID := req.PathParameters["accountId"]
	var body ReceiptBackfillRequest
	if req.RequestContext.HTTP.Method != "POST" || !validPortalID(accountID) || decodeAdmin(req, &body) != nil || !validPortalID(body.CommandID) || (body.Cursor != "" && !validPortalID(body.Cursor)) || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	result, err := a.backfillReceipts(ctx, accountID, body, domain.Audit{CommandID: body.CommandID, Actor: actor, Reason: strings.TrimSpace(body.Reason), RecordedAt: time.Now().UTC()})
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, result, map[string]string{"cache-control": "no-store"})
}

func (a AdminApp) backfillReceipts(ctx context.Context, accountID string, input ReceiptBackfillRequest, audit domain.Audit) (ReceiptBackfillResult, error) {
	account, err := a.Accounts.GetAccount(ctx, accountID)
	if err != nil {
		return ReceiptBackfillResult{}, err
	}
	member, err := a.Accounts.read(ctx, "TRIP#"+account.TripID, "MEMBER#"+accountID)
	if err != nil || member.Roster == nil || strings.TrimSpace(member.Roster.Name) == "" || strings.TrimSpace(member.Roster.Document) == "" {
		return ReceiptBackfillResult{}, domain.ErrInvalid
	}
	db, ok := a.Accounts.DB.(queryDatabase)
	if !ok {
		return ReceiptBackfillResult{}, errors.New("query unavailable")
	}
	pk := "ACCOUNT#" + accountID
	query := &dynamodb.QueryInput{TableName: &a.Accounts.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}, ":prefix": &types.AttributeValueMemberS{Value: "RECEIPT#"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(10)}
	if input.Cursor != "" {
		query.ExclusiveStartKey = key(pk, "RECEIPT#"+input.Cursor)
	}
	out, err := db.Query(ctx, query)
	if err != nil {
		return ReceiptBackfillResult{}, err
	}
	result := ReceiptBackfillResult{Apply: input.Apply}
	for _, item := range out.Items {
		var projection record
		if attributevalue.UnmarshalMap(item, &projection) != nil || !validPortalID(projection.ReceiptID) {
			return ReceiptBackfillResult{}, domain.ErrInvalid
		}
		root, readErr := a.Accounts.read(ctx, "RECEIPT#"+projection.ReceiptID, "META")
		if readErr != nil || root.Event == nil || root.Event.AccountID != accountID {
			return ReceiptBackfillResult{}, domain.ErrInvalid
		}
		if receiptDocumentVersion(root) >= currentReceiptDocumentVersion {
			continue
		}
		result.Eligible++
		if !input.Apply {
			continue
		}
		commandID := paymentOperationID("receipt-backfill:" + input.CommandID + ":" + projection.ReceiptID)
		if _, replayErr := a.Accounts.read(ctx, "COMMAND#"+commandID, "META"); replayErr == nil {
			result.Enqueued++
			continue
		}
		root.DocumentVersion = currentReceiptDocumentVersion
		root.PassengerName = strings.TrimSpace(member.Roster.Name)
		root.PassengerDocument = strings.TrimSpace(member.Roster.Document)
		root.DocumentKey = ""
		root.DocumentSHA256 = ""
		root.Status = "PENDING_DOCUMENT"
		root.DeliveryMode = "DOCUMENT_ONLY"
		root.RetryAudit = &audit
		rootWrite, _ := a.Accounts.put(root, "attribute_not_exists(documentVersion) OR documentVersion < :current", map[string]types.AttributeValue{":current": &types.AttributeValueMemberN{Value: "3"}})
		jobWrite, _ := a.Accounts.put(record{PK: "JOB#" + audit.RecordedAt.Format(time.DateOnly), SK: "PENDING#" + projection.ReceiptID, ReceiptID: projection.ReceiptID, Status: "PENDING", DeliveryMode: "DOCUMENT_ONLY"}, "attribute_not_exists(pk)", nil)
		commandWrite, _ := a.Accounts.put(record{PK: "COMMAND#" + commandID, SK: "META", ReceiptID: projection.ReceiptID, Status: "QUEUED", RetryAudit: &audit}, "attribute_not_exists(pk)", nil)
		if _, writeErr := a.Accounts.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{rootWrite, jobWrite, commandWrite}}); writeErr != nil {
			if _, replayErr := a.Accounts.read(ctx, "COMMAND#"+commandID, "META"); replayErr != nil {
				return ReceiptBackfillResult{}, domain.ErrConflict
			}
		}
		result.Enqueued++
	}
	if sk, ok := out.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
		result.NextCursor = strings.TrimPrefix(sk.Value, "RECEIPT#")
	}
	return result, nil
}

func ReceiptBackfillHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return app.HandleReceiptBackfill(ctx, req)
}
