package domain

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/oklog/ulid/v2"
)

type BulkKind string
type BulkState string

const (
	BulkMarkAllRead       BulkKind  = "mark_all_read"
	BulkRecalculateUnread BulkKind  = "recalculate_unread"
	BulkMaterializing     BulkState = "materializing"
	BulkApplying          BulkState = "applying"
	BulkCommitted         BulkState = "committed"
	BulkFailed            BulkState = "failed"
)

var (
	ErrBulkConflict = errors.New("bulk request conflict")
	ErrBulkNotFound = errors.New("bulk operation not found")
)

// BulkOperation is the durable status returned by bulk endpoints.
type BulkOperation struct {
	OperationID      string     `json:"operationId" dynamodbav:"operationId"`
	Kind             BulkKind   `json:"kind" dynamodbav:"kind"`
	State            BulkState  `json:"state" dynamodbav:"state"`
	Phase            *string    `json:"phase" dynamodbav:"phase"`
	InitiatorUserID  string     `json:"initiatorUserId" dynamodbav:"initiatorUserId"`
	Checkpoint       *string    `json:"checkpoint" dynamodbav:"checkpoint"`
	SnapshotComplete bool       `json:"snapshotComplete" dynamodbav:"snapshotComplete"`
	TargetCount      int        `json:"targetCount" dynamodbav:"targetCount"`
	Processed        int        `json:"processed" dynamodbav:"processed"`
	Applied          int        `json:"applied" dynamodbav:"applied"`
	Unchanged        int        `json:"unchanged" dynamodbav:"unchanged"`
	Failed           int        `json:"failed" dynamodbav:"failed"`
	CreatedAt        time.Time  `json:"createdAt" dynamodbav:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt" dynamodbav:"updatedAt"`
	CompletedAt      *time.Time `json:"completedAt" dynamodbav:"completedAt"`
	FailureCode      *string    `json:"failureCode" dynamodbav:"failureCode"`
}

type bulkDynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}
type BulkService struct {
	client    bulkDynamo
	tableName string
	now       func() time.Time
	newID     func(time.Time) (string, error)
}

func NewBulkService(client bulkDynamo, tableName string, now func() time.Time) (*BulkService, error) {
	if client == nil || tableName == "" {
		return nil, errors.New("bulk service configuration is required")
	}
	if now == nil {
		now = time.Now
	}
	return &BulkService{client: client, tableName: tableName, now: now, newID: func(at time.Time) (string, error) {
		id, err := ulid.New(ulid.Timestamp(at), rand.Reader)
		return id.String(), err
	}}, nil
}

// Start creates one global active operation per kind and is idempotent per caller key.
func (s *BulkService) Start(ctx context.Context, owner, idempotencyKey string, kind BulkKind) (BulkOperation, error) {
	requestPK := "BULK_REQUEST#" + owner + "#" + idempotencyKey
	if existingID, existingKind, found, err := s.readRequest(ctx, requestPK); err != nil {
		return BulkOperation{}, err
	} else if found {
		if existingKind != kind {
			return BulkOperation{}, ErrBulkConflict
		}
		return s.Get(ctx, existingID)
	}
	active, activeID, err := s.Active(ctx, kind)
	if err != nil {
		return BulkOperation{}, err
	}
	if active != nil {
		return *active, nil
	}
	now := s.now().UTC()
	operationID, err := s.newID(now)
	if err != nil {
		return BulkOperation{}, fmt.Errorf("create bulk operation id: %w", err)
	}
	phase := string(BulkMaterializing)
	operation := BulkOperation{OperationID: operationID, Kind: kind, State: BulkMaterializing, Phase: &phase, InitiatorUserID: owner, SnapshotComplete: false, CreatedAt: now, UpdatedAt: now}
	operationItem, err := attributevalue.MarshalMap(operation)
	if err != nil {
		return BulkOperation{}, fmt.Errorf("encode bulk operation: %w", err)
	}
	operationItem["pk"] = &types.AttributeValueMemberS{Value: "BULK#" + operationID}
	operationItem["sk"] = &types.AttributeValueMemberS{Value: "META"}
	activeKey := snapshotKey("BULK_ACTIVE#APP#INDOMITO_HUB", "KIND#"+string(kind))
	activeItem := cloneAttributeMap(activeKey)
	activeItem["operationId"] = &types.AttributeValueMemberS{Value: operationID}
	activeItem["state"] = &types.AttributeValueMemberS{Value: string(BulkMaterializing)}
	requestItem := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: requestPK}, "sk": &types.AttributeValueMemberS{Value: "META"}, "operationId": &types.AttributeValueMemberS{Value: operationID}, "kind": &types.AttributeValueMemberS{Value: string(kind)}}
	eventID, err := s.newID(now)
	if err != nil {
		return BulkOperation{}, err
	}
	shard := conversationShard(operationID)
	eventItem := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("WORK_DUE#OUTBOX#%02X", shard)}, "sk": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano) + "#" + eventID}, "eventId": &types.AttributeValueMemberS{Value: eventID}, "outboxType": &types.AttributeValueMemberS{Value: "workflow"}, "aggregate": &types.AttributeValueMemberS{Value: "BULK#" + operationID}, "operationId": &types.AttributeValueMemberS{Value: operationID}, "phase": &types.AttributeValueMemberS{Value: phase}, "dedupeKey": &types.AttributeValueMemberS{Value: "bulk:" + operationID + ":materializing:start"}, "state": &types.AttributeValueMemberS{Value: "pending"}, "nextAttemptAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(14 * 24 * time.Hour).Unix())}}
	activeCondition := "attribute_not_exists(pk)"
	activeValues := map[string]types.AttributeValue{}
	if activeID != "" {
		activeCondition = "operationId = :previous"
		activeValues[":previous"] = &types.AttributeValueMemberS{Value: activeID}
	}
	_, err = s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Put: &types.Put{TableName: &s.tableName, Item: operationItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}}, {Put: &types.Put{TableName: &s.tableName, Item: requestItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}}, {Put: &types.Put{TableName: &s.tableName, Item: activeItem, ConditionExpression: &activeCondition, ExpressionAttributeValues: activeValues}}, {Put: &types.Put{TableName: &s.tableName, Item: eventItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}}}})
	if err == nil {
		return operation, nil
	}
	if !isTransactionConflict(err) {
		return BulkOperation{}, fmt.Errorf("start bulk operation: %w", err)
	}
	if current, _, readErr := s.Active(ctx, kind); readErr == nil && current != nil {
		return *current, nil
	}
	return BulkOperation{}, ErrBulkConflict
}

func (s *BulkService) Get(ctx context.Context, operationID string) (BulkOperation, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.tableName, Key: snapshotKey("BULK#"+operationID, "META"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return BulkOperation{}, fmt.Errorf("read bulk operation: %w", err)
	}
	if len(out.Item) == 0 {
		return BulkOperation{}, ErrBulkNotFound
	}
	var operation BulkOperation
	if err = attributevalue.UnmarshalMap(out.Item, &operation); err != nil {
		return BulkOperation{}, fmt.Errorf("decode bulk operation: %w", err)
	}
	return operation, nil
}
func (s *BulkService) Active(ctx context.Context, kind BulkKind) (*BulkOperation, string, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.tableName, Key: snapshotKey("BULK_ACTIVE#APP#INDOMITO_HUB", "KIND#"+string(kind)), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return nil, "", fmt.Errorf("read active bulk mutex: %w", err)
	}
	idValue, ok := out.Item["operationId"].(*types.AttributeValueMemberS)
	if !ok || idValue.Value == "" {
		return nil, "", nil
	}
	operation, err := s.Get(ctx, idValue.Value)
	if errors.Is(err, ErrBulkNotFound) {
		return nil, idValue.Value, nil
	}
	if err != nil {
		return nil, "", err
	}
	if operation.State == BulkMaterializing || operation.State == BulkApplying {
		return &operation, idValue.Value, nil
	}
	return nil, idValue.Value, nil
}
func (s *BulkService) readRequest(ctx context.Context, pk string) (string, BulkKind, bool, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.tableName, Key: snapshotKey(pk, "META"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return "", "", false, fmt.Errorf("read bulk request: %w", err)
	}
	id, ok := out.Item["operationId"].(*types.AttributeValueMemberS)
	if !ok {
		return "", "", false, nil
	}
	kind, _ := out.Item["kind"].(*types.AttributeValueMemberS)
	if kind == nil {
		return "", "", false, errors.New("bulk request has no kind")
	}
	return id.Value, BulkKind(kind.Value), true, nil
}
