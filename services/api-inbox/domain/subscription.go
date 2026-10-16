package domain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"strings"
	"time"
)

var ErrSubscriptionRateLimited = errors.New("subscription ticket rate limited")

type Subscription struct {
	URL       string    `json:"url"`
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type subscriptionDynamo interface {
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}
type SubscriptionService struct {
	client                  subscriptionDynamo
	tableName, webSocketURL string
	now                     func() time.Time
	random                  func([]byte) (int, error)
}

func NewSubscriptionService(client subscriptionDynamo, tableName, webSocketURL string, now func() time.Time) (*SubscriptionService, error) {
	if client == nil || tableName == "" || !strings.HasPrefix(webSocketURL, "wss://") {
		return nil, errors.New("subscription service configuration is required")
	}
	if now == nil {
		now = time.Now
	}
	return &SubscriptionService{client: client, tableName: tableName, webSocketURL: strings.TrimRight(webSocketURL, "/"), now: now, random: rand.Read}, nil
}
func (s *SubscriptionService) Create(ctx context.Context, owner, scope, phone string) (Subscription, error) {
	now := s.now().UTC()
	raw := make([]byte, 32)
	if _, err := s.random(raw); err != nil {
		return Subscription{}, fmt.Errorf("create subscription ticket: %w", err)
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	expiresAt := now.Add(time.Minute)
	minute := now.Format("200601021504")
	ticketItem := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "TICKET#" + hash}, "sk": &types.AttributeValueMemberS{Value: "META"}, "ownerUserId": &types.AttributeValueMemberS{Value: owner}, "scope": &types.AttributeValueMemberS{Value: scope}, "phone": &types.AttributeValueMemberS{Value: phone}, "createdAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(expiresAt.Unix())}, "used": &types.AttributeValueMemberBOOL{Value: false}}
	rateKey := snapshotKey("RATE#TICKET#"+owner, "MINUTE#"+minute)
	_, err := s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Update: &types.Update{TableName: &s.tableName, Key: rateKey, UpdateExpression: aws.String("SET expiresAt=:expires ADD #count :one"), ConditionExpression: aws.String("attribute_not_exists(#count) OR #count < :limit"), ExpressionAttributeNames: map[string]string{"#count": "count"}, ExpressionAttributeValues: map[string]types.AttributeValue{":expires": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(2 * time.Minute).Unix())}, ":one": &types.AttributeValueMemberN{Value: "1"}, ":limit": &types.AttributeValueMemberN{Value: "5"}}}}, {Put: &types.Put{TableName: &s.tableName, Item: ticketItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}}}})
	if err != nil {
		if isTransactionConflict(err) {
			return Subscription{}, ErrSubscriptionRateLimited
		}
		return Subscription{}, fmt.Errorf("persist subscription ticket: %w", err)
	}
	return Subscription{URL: s.webSocketURL, Ticket: ticket, ExpiresAt: expiresAt}, nil
}
