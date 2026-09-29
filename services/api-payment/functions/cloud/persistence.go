package cloud

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Attempt conserva la prueba y el comprobante/evento en una escritura atómica.
type Attempt struct {
	PK                 string        `json:"-" dynamodbav:"pk"`
	SK                 string        `json:"-" dynamodbav:"sk"`
	ID                 string        `json:"id" dynamodbav:"id"`
	Owner              string        `json:"-" dynamodbav:"owner"`
	Amount             int64         `json:"amount" dynamodbav:"amount"`
	Status             string        `json:"status" dynamodbav:"status"`
	PaymentID          string        `json:"paymentId,omitempty" dynamodbav:"paymentId,omitempty"`
	PaymentURL         string        `json:"paymentUrl,omitempty" dynamodbav:"paymentUrl,omitempty"`
	CreatedAt          string        `json:"createdAt" dynamodbav:"createdAt"`
	Receipt            *Receipt      `json:"receipt,omitempty" dynamodbav:"receipt,omitempty"`
	Event              *PaymentEvent `json:"event,omitempty" dynamodbav:"event,omitempty"`
	NotificationStatus string        `json:"notificationStatus,omitempty" dynamodbav:"notificationStatus,omitempty"`
}

// Receipt es interno de prueba, no DTE ni ingreso real de caja.
type Receipt struct {
	ID          string `json:"id" dynamodbav:"id"`
	Amount      int64  `json:"amount" dynamodbav:"amount"`
	IssuedAt    string `json:"issuedAt" dynamodbav:"issuedAt"`
	Description string `json:"description" dynamodbav:"description"`
}

// PaymentEvent deja una salida versionada para procesamiento posterior.
type PaymentEvent struct {
	ID         string `json:"id" dynamodbav:"id"`
	Version    int    `json:"version" dynamodbav:"version"`
	Type       string `json:"type" dynamodbav:"type"`
	OccurredAt string `json:"occurredAt" dynamodbav:"occurredAt"`
}

var errNotFound = errors.New("prueba no encontrada")

func number(n int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(n, 10)}
}

func key(id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "TEST#" + id}, "sk": &types.AttributeValueMemberS{Value: "ATTEMPT"}}
}

func (a *App) get(ctx context.Context, id string) (Attempt, error) {
	var v Attempt
	out, err := a.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: &a.Table, Key: key(id), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return v, err
	}
	if len(out.Item) == 0 {
		return v, errNotFound
	}
	err = attributevalue.UnmarshalMap(out.Item, &v)
	return v, err
}

func conditional(err error) bool {
	var conflict *types.ConditionalCheckFailedException
	return errors.As(err, &conflict)
}

func (a *App) insert(ctx context.Context, v Attempt) error {
	item, err := attributevalue.MarshalMap(v)
	if err != nil {
		return err
	}
	_, err = a.DB.PutItem(ctx, &dynamodb.PutItemInput{TableName: &a.Table, Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")})
	return err
}

// replace limita transiciones para que concurrencia y reintentos no dupliquen efectos.
func (a *App) replace(ctx context.Context, v Attempt, previous string) error {
	item, err := attributevalue.MarshalMap(v)
	if err != nil {
		return err
	}
	_, err = a.DB.PutItem(ctx, &dynamodb.PutItemInput{TableName: &a.Table, Item: item,
		ConditionExpression:       aws.String("#status = :previous AND #owner = :owner"),
		ExpressionAttributeNames:  map[string]string{"#status": "status", "#owner": "owner"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":previous": &types.AttributeValueMemberS{Value: previous}, ":owner": &types.AttributeValueMemberS{Value: v.Owner}},
	})
	return err
}

func (a *App) quota(ctx context.Context, owner string) error {
	_, err := a.DB.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: &a.Table,
		Key:                       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "QUOTA#" + owner}, "sk": &types.AttributeValueMemberS{Value: a.Now().UTC().Format(time.DateOnly)}},
		UpdateExpression:          aws.String("SET expiresAt = :expiry ADD #count :one"),
		ConditionExpression:       aws.String("attribute_not_exists(#count) OR #count < :limit"),
		ExpressionAttributeNames:  map[string]string{"#count": "count"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":one": &types.AttributeValueMemberN{Value: "1"}, ":limit": &types.AttributeValueMemberN{Value: "10"}, ":expiry": number(a.Now().Add(48 * time.Hour).Unix())},
	})
	return err
}
