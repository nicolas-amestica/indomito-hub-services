package collection

import (
	"context"
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

type ReceiptFailureSummary struct {
	ReceiptID        string `json:"receiptId"`
	DeliveryID       string `json:"deliveryId,omitempty"`
	FailureCode      string `json:"failureCode"`
	DeliveryAttempts int64  `json:"deliveryAttempts"`
}
type ReceiptFailurePage struct {
	Items      []ReceiptFailureSummary `json:"items"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}
type ReceiptFailureRetryRequest struct {
	CommandID  string `json:"commandId"`
	DeliveryID string `json:"deliveryId,omitempty"`
	Reason     string `json:"reason"`
}

func (a AdminApp) HandleReceiptFailures(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	if req.RequestContext.HTTP.Method == "GET" {
		return a.listReceiptFailures(ctx, req)
	}
	if req.RequestContext.HTTP.Method != "POST" {
		return adminFailure(req, 405, "METHOD_NOT_ALLOWED")
	}
	receiptID := req.PathParameters["receiptId"]
	var body ReceiptFailureRetryRequest
	if !validPortalID(receiptID) || decodeAdmin(req, &body) != nil || !validPortalID(body.CommandID) || (body.DeliveryID != "" && !validPortalID(body.DeliveryID)) || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	audit := domain.Audit{CommandID: paymentOperationID("receipt-retry:" + receiptID + ":" + body.CommandID), Actor: actor, Reason: strings.TrimSpace(body.Reason), RecordedAt: time.Now().UTC()}
	view, err := a.retryReceiptFailure(ctx, receiptID, body.DeliveryID, audit)
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(202, view, map[string]string{"cache-control": "no-store"})
}

func (a AdminApp) listReceiptFailures(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	date, cursor := req.QueryStringParameters["date"], req.QueryStringParameters["cursor"]
	if _, err := time.Parse(time.DateOnly, date); err != nil || (cursor != "" && !validPortalID(cursor)) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	db, ok := a.Accounts.DB.(queryDatabase)
	if !ok {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	pk := "JOB#" + date
	in := &dynamodb.QueryInput{TableName: &a.Accounts.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}, ":prefix": &types.AttributeValueMemberS{Value: "PENDING#"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(20)}
	if cursor != "" {
		in.ExclusiveStartKey = key(pk, "PENDING#"+cursor)
	}
	out, err := db.Query(ctx, in)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	page := ReceiptFailurePage{Items: []ReceiptFailureSummary{}}
	for _, item := range out.Items {
		var job record
		if attributevalue.UnmarshalMap(item, &job) != nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		if job.Status != "DELIVERY_FAILED" {
			continue
		}
		target, readErr := a.receiptDelivery(ctx, job.ReceiptID, job.DeliveryID)
		if readErr != nil {
			return domainFailure(req, readErr)
		}
		page.Items = append(page.Items, ReceiptFailureSummary{ReceiptID: job.ReceiptID, DeliveryID: job.DeliveryID, FailureCode: safeReceiptFailure(target.LastFailureCode), DeliveryAttempts: target.DeliveryAttempts})
	}
	if sk, ok := out.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
		page.NextCursor = strings.TrimPrefix(sk.Value, "PENDING#")
	}
	return lambdautil.SuccessResponseWithHeaders(200, page, map[string]string{"cache-control": "no-store"})
}

func safeReceiptFailure(code string) string {
	switch code {
	case "RECEIPT_EMAIL_MISSING", "IMMUTABLE_RECEIPT_CONFLICT", "SMTP_NOT_ACCEPTED":
		return code
	default:
		return "DELIVERY_DEPENDENCY_FAILURE"
	}
}
func (a AdminApp) receiptDelivery(ctx context.Context, receiptID, deliveryID string) (record, error) {
	if deliveryID == "" {
		return a.Accounts.read(ctx, "RECEIPT#"+receiptID, "META")
	}
	return a.Accounts.read(ctx, "RECEIPT#"+receiptID, "DELIVERY#"+deliveryID)
}

func (a AdminApp) retryReceiptFailure(ctx context.Context, receiptID, deliveryID string, audit domain.Audit) (ReceiptFailureSummary, error) {
	commandPK := "COMMAND#" + audit.CommandID
	if prior, err := a.Accounts.read(ctx, commandPK, "META"); err == nil {
		return ReceiptFailureSummary{ReceiptID: prior.ReceiptID, DeliveryID: prior.DeliveryID, FailureCode: prior.LastFailureCode}, nil
	}
	current, err := a.receiptDelivery(ctx, receiptID, deliveryID)
	if err != nil {
		return ReceiptFailureSummary{}, err
	}
	if current.Status != "DELIVERY_FAILED" || current.ReceiptID != receiptID {
		return ReceiptFailureSummary{}, domain.ErrConflict
	}
	current.Status = "PENDING_DOCUMENT"
	current.DeliveryAttempts = 0
	current.LastFailureCode = ""
	current.RetryAudit = &audit
	receiptWrite, err := a.Accounts.put(current, "#status = :failed", map[string]types.AttributeValue{":failed": &types.AttributeValueMemberS{Value: "DELIVERY_FAILED"}})
	if err != nil {
		return ReceiptFailureSummary{}, err
	}
	id := receiptID
	if deliveryID != "" {
		id = deliveryID
	}
	date := audit.RecordedAt.Format(time.DateOnly)
	jobWrite, err := a.Accounts.put(record{PK: "JOB#" + date, SK: "PENDING#" + id, ReceiptID: receiptID, DeliveryID: deliveryID, Status: "PENDING", RetryAudit: &audit}, "attribute_not_exists(pk) OR #status = :failed", map[string]types.AttributeValue{":failed": &types.AttributeValueMemberS{Value: "DELIVERY_FAILED"}})
	if err != nil {
		return ReceiptFailureSummary{}, err
	}
	commandWrite, err := a.Accounts.put(record{PK: commandPK, SK: "META", ReceiptID: receiptID, DeliveryID: deliveryID, Status: "QUEUED", RetryAudit: &audit}, "attribute_not_exists(pk)", nil)
	if err != nil {
		return ReceiptFailureSummary{}, err
	}
	_, err = a.Accounts.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{receiptWrite, jobWrite, commandWrite}})
	if err != nil {
		if saved, readErr := a.Accounts.read(ctx, commandPK, "META"); readErr == nil {
			return ReceiptFailureSummary{ReceiptID: saved.ReceiptID, DeliveryID: saved.DeliveryID}, nil
		}
		return ReceiptFailureSummary{}, domain.ErrConflict
	}
	return ReceiptFailureSummary{ReceiptID: receiptID, DeliveryID: deliveryID}, nil
}

func ReceiptFailuresHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return app.HandleReceiptFailures(ctx, req)
}
