package domain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type ContactUpdate struct {
	Phone string `json:"phone"`
	Name  string `json:"name"`
}
type ContactService struct {
	client interface {
		UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	}
	tableName string
	now       func() time.Time
}

func NewContactService(client interface {
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}, tableName string, now func() time.Time) (*ContactService, error) {
	if client == nil || tableName == "" {
		return nil, errors.New("contact service configuration is required")
	}
	if now == nil {
		now = time.Now
	}
	return &ContactService{client: client, tableName: tableName, now: now}, nil
}
func (s *ContactService) Update(ctx context.Context, phone, name string) (ContactUpdate, error) {
	now := s.now().UTC()
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: &s.tableName, Key: snapshotKey("CONV#"+phone, "META"), UpdateExpression: aws.String("SET alias=:alias, updatedAt=:updated, expiresAt=:expires ADD generation :one"), ExpressionAttributeValues: map[string]types.AttributeValue{":alias": &types.AttributeValueMemberS{Value: name}, ":updated": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, ":expires": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(90 * 24 * time.Hour).Unix())}, ":one": &types.AttributeValueMemberN{Value: "1"}}, ConditionExpression: aws.String("attribute_exists(pk)")})
	if err != nil {
		var conditional *types.ConditionalCheckFailedException
		if errors.As(err, &conditional) {
			return ContactUpdate{}, ErrConversationNotFound
		}
		return ContactUpdate{}, fmt.Errorf("update inbox contact: %w", err)
	}
	return ContactUpdate{Phone: phone, Name: name}, nil
}
