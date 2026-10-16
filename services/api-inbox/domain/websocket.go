package domain

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var ErrWebSocketTicketInvalid = errors.New("websocket ticket invalid")

type webSocketDynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

// WebSocketConnectionService exchanges one-use tickets and owns connection projections.
type WebSocketConnectionService struct {
	client    webSocketDynamo
	tableName string
	now       func() time.Time
}

// NewWebSocketConnectionService creates the connection lifecycle service.
func NewWebSocketConnectionService(client webSocketDynamo, tableName string, now func() time.Time) (*WebSocketConnectionService, error) {
	if client == nil || tableName == "" {
		return nil, errors.New("websocket connection configuration is required")
	}
	if now == nil {
		now = time.Now
	}
	return &WebSocketConnectionService{client: client, tableName: tableName, now: now}, nil
}

// Connect atomically consumes a ticket and registers both connection and subscription lookup.
func (s *WebSocketConnectionService) Connect(ctx context.Context, connectionID, ticket string) error {
	raw, err := base64.RawURLEncoding.DecodeString(ticket)
	if err != nil || len(raw) != 32 || connectionID == "" {
		return ErrWebSocketTicketInvalid
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	ticketOutput, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.tableName, Key: snapshotKey("TICKET#"+hash, "META"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return fmt.Errorf("read websocket ticket: %w", err)
	}
	owner, ownerOK := ticketOutput.Item["ownerUserId"].(*types.AttributeValueMemberS)
	scope, scopeOK := ticketOutput.Item["scope"].(*types.AttributeValueMemberS)
	phone, _ := ticketOutput.Item["phone"].(*types.AttributeValueMemberS)
	if !ownerOK || !scopeOK || (scope.Value != "list" && scope.Value != "detail") || (scope.Value == "detail" && (phone == nil || phone.Value == "")) {
		return ErrWebSocketTicketInvalid
	}
	now := s.now().UTC()
	subscriptionPK := "WS_SUB#LIST"
	phoneValue := ""
	if phone != nil {
		phoneValue = phone.Value
	}
	if scope.Value == "detail" {
		subscriptionPK = "WS_SUB#DETAIL#" + phoneValue
	}
	expiresAt := now.Add(24 * time.Hour).Unix()
	connection := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "WS_CONN#" + connectionID}, "sk": &types.AttributeValueMemberS{Value: "META"}, "connectionId": &types.AttributeValueMemberS{Value: connectionID}, "ownerUserId": owner, "scope": scope, "phone": &types.AttributeValueMemberS{Value: phoneValue}, "subscriptionPk": &types.AttributeValueMemberS{Value: subscriptionPK}, "connectedAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(expiresAt)}}
	subscription := cloneAttributeMap(connection)
	subscription["pk"], subscription["sk"] = &types.AttributeValueMemberS{Value: subscriptionPK}, &types.AttributeValueMemberS{Value: "CONN#" + connectionID}
	_, err = s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Update: &types.Update{TableName: &s.tableName, Key: snapshotKey("TICKET#"+hash, "META"), UpdateExpression: aws.String("SET used=:true, usedAt=:now"), ConditionExpression: aws.String("used=:false AND expiresAt >= :epoch"), ExpressionAttributeValues: map[string]types.AttributeValue{":true": &types.AttributeValueMemberBOOL{Value: true}, ":false": &types.AttributeValueMemberBOOL{Value: false}, ":now": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, ":epoch": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Unix())}}}},
		{Put: &types.Put{TableName: &s.tableName, Item: connection, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: &s.tableName, Item: subscription, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}})
	if err != nil {
		if isTransactionConflict(err) {
			return ErrWebSocketTicketInvalid
		}
		return fmt.Errorf("register websocket connection: %w", err)
	}
	return nil
}

// Disconnect removes a connection and its subscription projection idempotently.
func (s *WebSocketConnectionService) Disconnect(ctx context.Context, connectionID string) error {
	key := snapshotKey("WS_CONN#"+connectionID, "META")
	output, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.tableName, Key: key, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return fmt.Errorf("read websocket connection: %w", err)
	}
	if len(output.Item) == 0 {
		return nil
	}
	subscriptionPK, ok := output.Item["subscriptionPk"].(*types.AttributeValueMemberS)
	if !ok || subscriptionPK.Value == "" {
		return errors.New("websocket connection has no subscription projection")
	}
	_, err = s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Delete: &types.Delete{TableName: &s.tableName, Key: key}},
		{Delete: &types.Delete{TableName: &s.tableName, Key: snapshotKey(subscriptionPK.Value, "CONN#"+connectionID)}},
	}})
	if err != nil {
		return fmt.Errorf("remove websocket connection: %w", err)
	}
	return nil
}
