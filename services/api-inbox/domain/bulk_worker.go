package domain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/oklog/ulid/v2"
)

type bulkWorkerDynamo interface {
	bulkDynamo
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// BulkWorker applies a durable bulk operation over a frozen list cutoff.
type BulkWorker struct {
	client    bulkWorkerDynamo
	tableName string
	now       func() time.Time
}

// NewBulkWorker creates the workflow worker.
func NewBulkWorker(client bulkWorkerDynamo, tableName string, now func() time.Time) (*BulkWorker, error) {
	if client == nil || tableName == "" {
		return nil, errors.New("bulk worker configuration is required")
	}
	if now == nil {
		now = time.Now
	}
	return &BulkWorker{client: client, tableName: tableName, now: now}, nil
}

// Process completes an operation idempotently; repeated deliveries observe terminal state.
func (w *BulkWorker) Process(ctx context.Context, operationID string) error {
	service, err := NewBulkService(w.client, w.tableName, w.now)
	if err != nil {
		return err
	}
	operation, err := service.Get(ctx, operationID)
	if err != nil {
		return err
	}
	if operation.State == BulkCommitted {
		return nil
	}
	if operation.State == BulkFailed {
		return errors.New("bulk operation is terminally failed")
	}
	cutoff, err := ReadListCutoff(ctx, w.client, w.tableName)
	if err != nil {
		return err
	}
	items, err := MaterializeConversationList(ctx, w.client, w.tableName, cutoff, "", 10000)
	if err != nil {
		return err
	}
	mutation, err := NewConversationMutationService(w.client, w.tableName, w.now)
	if err != nil {
		return err
	}
	applied, unchanged := 0, 0
	for _, item := range items {
		target := 0
		if operation.Kind == BulkRecalculateUnread {
			target, err = w.recalculateUnread(ctx, item.Phone)
			if err != nil {
				return err
			}
		}
		if target == item.UnreadCount {
			unchanged++
			continue
		}
		if _, err = mutation.SetUnreadCount(ctx, item.Phone, target); err != nil {
			return err
		}
		applied++
	}
	now := w.now().UTC()
	values := map[string]types.AttributeValue{":state": &types.AttributeValueMemberS{Value: string(BulkCommitted)}, ":phase": &types.AttributeValueMemberS{Value: "committed"}, ":complete": &types.AttributeValueMemberBOOL{Value: true}, ":target": &types.AttributeValueMemberN{Value: fmt.Sprint(len(items))}, ":processed": &types.AttributeValueMemberN{Value: fmt.Sprint(len(items))}, ":applied": &types.AttributeValueMemberN{Value: fmt.Sprint(applied)}, ":unchanged": &types.AttributeValueMemberN{Value: fmt.Sprint(unchanged)}, ":now": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}}
	completionSequence, err := ReadListCutoff(ctx, w.client, w.tableName)
	if err != nil {
		return err
	}
	eventID := ulid.Make().String()
	payload, err := attributevalue.MarshalMap(map[string]any{"operationId": operationID, "kind": operation.Kind, "state": BulkCommitted, "targetCount": len(items), "applied": applied, "unchanged": unchanged})
	if err != nil {
		return fmt.Errorf("encode bulk completion event: %w", err)
	}
	event := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("WORK_DUE#OUTBOX#%02X", conversationShard(operationID))}, "sk": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano) + "#" + eventID}, "eventId": &types.AttributeValueMemberS{Value: eventID}, "eventType": &types.AttributeValueMemberS{Value: "bulk.updated"}, "stream": &types.AttributeValueMemberS{Value: "list"}, "sequence": &types.AttributeValueMemberN{Value: fmt.Sprint(completionSequence + 1)}, "payload": &types.AttributeValueMemberM{Value: payload}, "state": &types.AttributeValueMemberS{Value: "pending"}, "nextAttemptAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(14 * 24 * time.Hour).Unix())}}
	_, err = w.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		sequenceUpdate(w.tableName, listSequencePK, completionSequence, completionSequence+1),
		{Update: &types.Update{TableName: &w.tableName, Key: snapshotKey("BULK#"+operationID, "META"), UpdateExpression: aws.String("SET #state=:state, phase=:phase, snapshotComplete=:complete, targetCount=:target, processed=:processed, applied=:applied, unchanged=:unchanged, updatedAt=:now, completedAt=:now"), ConditionExpression: aws.String("#state <> :state"), ExpressionAttributeNames: map[string]string{"#state": "state"}, ExpressionAttributeValues: values}},
		{Delete: &types.Delete{TableName: &w.tableName, Key: snapshotKey("BULK_ACTIVE#APP#INDOMITO_HUB", "KIND#"+string(operation.Kind)), ConditionExpression: aws.String("operationId=:operation"), ExpressionAttributeValues: map[string]types.AttributeValue{":operation": &types.AttributeValueMemberS{Value: operationID}}}},
		{Put: &types.Put{TableName: &w.tableName, Item: event, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}})
	if err != nil {
		return fmt.Errorf("complete bulk operation: %w", err)
	}
	return nil
}

func (w *BulkWorker) recalculateUnread(ctx context.Context, phone string) (int, error) {
	condition := "pk=:pk AND begins_with(sk,:message)"
	count := 0
	var start map[string]types.AttributeValue
	for {
		output, err := w.client.Query(ctx, &dynamodb.QueryInput{TableName: &w.tableName, KeyConditionExpression: &condition, ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "CONV#" + phone}, ":message": &types.AttributeValueMemberS{Value: "MSG#"}}, ScanIndexForward: aws.Bool(false), ExclusiveStartKey: start})
		if err != nil {
			return 0, fmt.Errorf("read messages for unread recalculation: %w", err)
		}
		var messages []Message
		if err = attributevalue.UnmarshalListOfMaps(output.Items, &messages); err != nil {
			return 0, fmt.Errorf("decode messages for unread recalculation: %w", err)
		}
		for _, message := range messages {
			if message.Type == MessageTypeTeam {
				return count, nil
			}
			if message.Type == MessageTypeCustomer {
				count++
			}
		}
		if len(output.LastEvaluatedKey) == 0 {
			return count, nil
		}
		start = output.LastEvaluatedKey
	}
}
