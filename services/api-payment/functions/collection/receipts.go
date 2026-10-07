package collection

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
)

// ReceiptSigner crea enlaces temporales solo después de verificar pertenencia.
type ReceiptSigner interface {
	Download(context.Context, string) (string, error)
}

// ReceiptsApp comparte documentos, nunca los correos ni motivos de auditoría.
type ReceiptsApp struct {
	Accounts Service
	Signer   ReceiptSigner
	Now      func() time.Time
}

// ReceiptSummary es la proyección mínima listable de un ingreso persistido.
type ReceiptSummary struct {
	ID            string `json:"id"`
	Amount        int64  `json:"amount"`
	EffectiveDate string `json:"effectiveDate"`
	Review        bool   `json:"review"`
}

// ReceiptPage pagina por claves directas de una cuenta, sin GSI ni consulta global.
type ReceiptPage struct {
	Items      []ReceiptSummary `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

func (a ReceiptsApp) accountID(ctx context.Context, req events.APIGatewayV2HTTPRequest) (string, error) {
	if req.RequestContext.Authorizer == nil {
		return "", ErrNotFound
	}
	if req.RequestContext.Authorizer.Lambda["paymentAccess"] != "admin" {
		return "", ErrNotFound
	}
	if _, err := lambdautil.UserIDFromContext(req); err != nil {
		return "", ErrNotFound
	}
	id := req.PathParameters["accountId"]
	if !validPortalID(id) {
		return "", ErrNotFound
	}
	account, err := a.Accounts.GetAccount(ctx, id)
	return account.ID, err
}

// HandleList no expone las claves DynamoDB ni acepta una cuenta pública en query/body.
func (a ReceiptsApp) HandleList(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if req.RequestContext.HTTP.Method != "GET" {
		return portalFailure(405)
	}
	id, err := a.accountID(ctx, req)
	if err != nil {
		return portalFailure(403)
	}
	cursor := req.QueryStringParameters["cursor"]
	if cursor != "" && !validPortalID(cursor) {
		return portalFailure(400)
	}
	db, ok := a.Accounts.DB.(queryDatabase)
	if !ok {
		return portalFailure(503)
	}
	input := &dynamodb.QueryInput{TableName: &a.Accounts.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "ACCOUNT#" + id}, ":prefix": &types.AttributeValueMemberS{Value: "RECEIPT#"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(20)}
	if cursor != "" {
		input.ExclusiveStartKey = key("ACCOUNT#"+id, "RECEIPT#"+cursor)
	}
	result, err := db.Query(ctx, input)
	if err != nil {
		return portalFailure(503)
	}
	page := ReceiptPage{Items: []ReceiptSummary{}}
	for _, item := range result.Items {
		var row record
		if attributevalue.UnmarshalMap(item, &row) != nil || row.AccountID != id || row.Event == nil || !validPortalID(row.ReceiptID) {
			return portalFailure(503)
		}
		page.Items = append(page.Items, ReceiptSummary{ID: row.ReceiptID, Amount: row.Event.Amount, EffectiveDate: row.Event.EffectiveDate, Review: row.Event.Type == "PAYMENT_REQUIRES_REVIEW"})
	}
	if sk, ok := result.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
		page.NextCursor = strings.TrimPrefix(sk.Value, "RECEIPT#")
	}
	return portalResponse(200, page)
}

var receiptSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)

const currentReceiptDocumentVersion int64 = 3

func receiptDocumentVersion(row record) int64 {
	if row.DocumentVersion == 2 || row.DocumentVersion == 3 {
		return row.DocumentVersion
	}
	return 1
}

// HandleDownload verifica código vigente, cuenta y documento privado antes de firmar por dos minutos.
func (a ReceiptsApp) HandleDownload(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if req.RequestContext.HTTP.Method != "GET" {
		return portalFailure(405)
	}
	accountID, err := a.accountID(ctx, req)
	if err != nil {
		return portalFailure(403)
	}
	id := req.PathParameters["id"]
	if !validPortalID(id) {
		return portalFailure(404)
	}
	r, err := a.Accounts.read(ctx, "RECEIPT#"+id, "META")
	if errors.Is(err, ErrNotFound) {
		return portalFailure(404)
	}
	if err != nil {
		return portalFailure(503)
	}
	if r.ReceiptID != id || r.Event == nil || r.Event.AccountID != accountID {
		return portalFailure(404)
	}
	if r.DocumentKey != "receipts/"+accountID+"/"+id+"/v"+strconv.FormatInt(receiptDocumentVersion(r), 10)+".pdf" || !receiptSHA.MatchString(r.DocumentSHA256) {
		return portalFailure(409)
	}
	if a.Signer == nil {
		return portalFailure(503)
	}
	url, err := a.Signer.Download(ctx, r.DocumentKey)
	if err != nil {
		return portalFailure(503)
	}
	return portalResponse(200, map[string]any{"url": url, "expiresIn": 120})
}
