package collection

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// HandleGroupList pagina comprobantes grupales por la partición de la gira.
func (a ReceiptsApp) HandleGroupList(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	tripID := req.PathParameters["tripId"]
	if req.RequestContext.HTTP.Method != "GET" || !validPortalID(tripID) {
		return portalFailure(400)
	}
	if _, denied := annexAdminIdentity(req); denied != nil {
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
	pk := "TRIP#" + tripID
	input := &dynamodb.QueryInput{TableName: &a.Accounts.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}, ":prefix": &types.AttributeValueMemberS{Value: "RECEIPT#"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(20)}
	if cursor != "" {
		input.ExclusiveStartKey = key(pk, "RECEIPT#"+cursor)
	}
	result, err := db.Query(ctx, input)
	if err != nil {
		return portalFailure(503)
	}
	page := ReceiptPage{Items: []ReceiptSummary{}}
	for _, item := range result.Items {
		var row record
		if attributevalue.UnmarshalMap(item, &row) != nil || row.TripID != tripID || row.Event == nil || row.Event.Type != "GROUP_DEPOSIT_RECEIVED" || !validPortalID(row.ReceiptID) {
			return portalFailure(503)
		}
		page.Items = append(page.Items, ReceiptSummary{ID: row.ReceiptID, Amount: row.Event.Amount, EffectiveDate: row.Event.EffectiveDate})
	}
	if sk, ok := result.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
		page.NextCursor = strings.TrimPrefix(sk.Value, "RECEIPT#")
	}
	return portalResponse(200, page)
}

// HandleGroupDownload firma exclusivamente la ruta canónica del comprobante grupal.
func (a ReceiptsApp) HandleGroupDownload(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	tripID, id := req.PathParameters["tripId"], req.PathParameters["id"]
	if req.RequestContext.HTTP.Method != "GET" || !validPortalID(tripID) || !validPortalID(id) {
		return portalFailure(404)
	}
	if _, denied := annexAdminIdentity(req); denied != nil {
		return portalFailure(403)
	}
	projection, err := a.Accounts.read(ctx, "TRIP#"+tripID, "RECEIPT#"+id)
	if errors.Is(err, ErrNotFound) {
		return portalFailure(404)
	}
	if err != nil || projection.TripID != tripID || projection.ReceiptID != id || projection.Event == nil || projection.Event.Type != "GROUP_DEPOSIT_RECEIVED" {
		return portalFailure(503)
	}
	receipt, err := a.Accounts.read(ctx, "RECEIPT#"+id, "META")
	if errors.Is(err, ErrNotFound) {
		return portalFailure(404)
	}
	if err != nil {
		return portalFailure(503)
	}
	if receipt.ReceiptID != id || receipt.Event == nil || receipt.Event.TripID != tripID || receipt.DocumentKey != "receipts/groups/"+tripID+"/"+id+"/v"+strconv.FormatInt(receiptDocumentVersion(receipt), 10)+".pdf" || !receiptSHA.MatchString(receipt.DocumentSHA256) {
		return portalFailure(409)
	}
	if a.Signer == nil {
		return portalFailure(503)
	}
	url, err := a.Signer.Download(ctx, receipt.DocumentKey)
	if err != nil {
		return portalFailure(503)
	}
	return portalResponse(200, map[string]any{"url": url, "expiresIn": 120})
}
