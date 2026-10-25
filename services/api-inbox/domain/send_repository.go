package domain

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/oklog/ulid/v2"
)

type sendDynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

type DynamoSendRepository struct {
	client    sendDynamo
	tableName string
}

func NewDynamoSendRepository(client sendDynamo, tableName string) (*DynamoSendRepository, error) {
	if client == nil || tableName == "" {
		return nil, errors.New("send repository configuration is required")
	}
	return &DynamoSendRepository{client: client, tableName: tableName}, nil
}

func (r *DynamoSendRepository) LastCustomerMessageAt(ctx context.Context, phone string) (*time.Time, error) {
	condition := "pk = :pk AND begins_with(sk, :message)"
	out, err := r.client.Query(ctx, &dynamodb.QueryInput{TableName: &r.tableName, ConsistentRead: aws.Bool(true), KeyConditionExpression: &condition, ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "CONV#" + phone}, ":message": &types.AttributeValueMemberS{Value: "MSG#"}}, ScanIndexForward: aws.Bool(false), Limit: aws.Int32(30)})
	if err != nil {
		return nil, fmt.Errorf("query service window: %w", err)
	}
	var messages []Message
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &messages); err != nil {
		return nil, fmt.Errorf("decode service window: %w", err)
	}
	for _, message := range messages {
		if message.Type == MessageTypeCustomer {
			value := message.CreatedAt
			return &value, nil
		}
	}
	return nil, nil
}

type sendRecord struct {
	PK          string              `dynamodbav:"pk"`
	SK          string              `dynamodbav:"sk"`
	OwnerID     string              `dynamodbav:"ownerUserId"`
	RequestHash string              `dynamodbav:"requestHash"`
	Content     string              `dynamodbav:"content"`
	Actor       MessageType         `dynamodbav:"actor,omitempty"`
	ReplyTo     *MessageReference   `dynamodbav:"replyTo,omitempty"`
	Template    *SendTemplate       `dynamodbav:"template,omitempty"`
	Rich        *MessageRichContent `dynamodbav:"rich,omitempty"`
	SendState
	ExpiresAt int64 `dynamodbav:"expiresAt"`
}

func (r *DynamoSendRepository) MessageReference(ctx context.Context, phone, id string) (*MessageReference, error) {
	out, err := r.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &r.tableName, Key: snapshotKey("CONV#"+phone, "MSG#"+id), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return nil, fmt.Errorf("read reply target: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, ErrReplyTargetInvalid
	}
	var message Message
	if err = attributevalue.UnmarshalMap(out.Item, &message); err != nil {
		return nil, fmt.Errorf("decode reply target: %w", err)
	}
	if message.WhatsAppMessageID == nil || strings.TrimSpace(*message.WhatsAppMessageID) == "" {
		return nil, ErrReplyTargetInvalid
	}
	return &MessageReference{ID: message.ID, Type: message.Type, Content: message.Content, WhatsAppMessageID: *message.WhatsAppMessageID}, nil
}

func (r *DynamoSendRepository) Reserve(ctx context.Context, reservation SendReservation) (SendState, bool, error) {
	preview := sanitizeSendText(reservation.Content)
	state := SendState{ClientMessageID: reservation.ClientMessageID, Phone: reservation.Phone, State: SendReserved, CreatedAt: reservation.Now, UpdatedAt: reservation.Now, RetryCount: 0, Preview: &preview, Attachment: reservation.Attachment}
	record := sendRecord{PK: "SEND#" + reservation.ClientMessageID, SK: "META", OwnerID: reservation.OwnerID, RequestHash: reservation.RequestHash, Content: reservation.Content, Actor: reservation.Actor, ReplyTo: reservation.ReplyTo, Template: reservation.Template, Rich: reservation.Rich, SendState: state, ExpiresAt: reservation.Now.Add(90 * 24 * time.Hour).Unix()}
	item, err := attributevalue.MarshalMap(record)
	if err != nil {
		return SendState{}, false, fmt.Errorf("encode send reservation: %w", err)
	}
	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: &r.tableName, Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")})
	if err == nil {
		if err = r.putView(ctx, reservation.OwnerID, state); err != nil {
			return SendState{}, false, err
		}
		return state, true, nil
	}
	var conditional *types.ConditionalCheckFailedException
	if !errors.As(err, &conditional) {
		return SendState{}, false, fmt.Errorf("reserve send: %w", err)
	}
	existing, existingRecord, readErr := r.read(ctx, reservation.ClientMessageID)
	if readErr != nil {
		return SendState{}, false, readErr
	}
	if existingRecord.OwnerID != reservation.OwnerID || existingRecord.Phone != reservation.Phone || existingRecord.RequestHash != reservation.RequestHash {
		return SendState{}, false, ErrSendConflict
	}
	return existing, false, nil
}

func (r *DynamoSendRepository) MarkProviderPending(ctx context.Context, ownerID, id string, now time.Time) (SendState, error) {
	return r.transition(ctx, ownerID, id, SendProviderPending, "", now)
}

func (r *DynamoSendRepository) Owned(ctx context.Context, ownerID, id string) (SendState, error) {
	state, record, err := r.read(ctx, id)
	if err != nil {
		return SendState{}, err
	}
	if record.OwnerID != ownerID {
		return SendState{}, ErrSendNotFound
	}
	return state, nil
}

func (r *DynamoSendRepository) ClaimRetry(ctx context.Context, ownerID, id string, now time.Time) (SendState, string, *MessageReference, *SendTemplate, *MessageRichContent, error) {
	state, record, err := r.read(ctx, id)
	if err != nil {
		return SendState{}, "", nil, nil, nil, err
	}
	if record.OwnerID != ownerID {
		return SendState{}, "", nil, nil, nil, ErrSendNotFound
	}
	if state.RetryCount >= 1 {
		return SendState{}, "", nil, nil, nil, ErrSendRetryExhausted
	}
	if state.State != SendFailedRetryable {
		return SendState{}, "", nil, nil, nil, ErrSendNotRetryable
	}
	state.State, state.RetryCount, state.UpdatedAt = SendProviderPending, 1, now
	state.Error, state.RetryAvailable = nil, nil
	_, err = r.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: &r.tableName, Key: snapshotKey("SEND#"+id, "META"), UpdateExpression: aws.String("SET #state=:pending, retryCount=:one, updatedAt=:updated REMOVE #error, retryAvailable"), ConditionExpression: aws.String("#state=:retryable AND retryCount=:zero AND ownerUserId=:owner"), ExpressionAttributeNames: map[string]string{"#state": "state", "#error": "error"}, ExpressionAttributeValues: map[string]types.AttributeValue{":pending": &types.AttributeValueMemberS{Value: string(SendProviderPending)}, ":retryable": &types.AttributeValueMemberS{Value: string(SendFailedRetryable)}, ":one": &types.AttributeValueMemberN{Value: "1"}, ":zero": &types.AttributeValueMemberN{Value: "0"}, ":updated": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, ":owner": &types.AttributeValueMemberS{Value: ownerID}}})
	if err != nil {
		var conditional *types.ConditionalCheckFailedException
		if errors.As(err, &conditional) {
			return SendState{}, "", nil, nil, nil, ErrSendNotRetryable
		}
		return SendState{}, "", nil, nil, nil, fmt.Errorf("claim send retry: %w", err)
	}
	if err = r.putView(ctx, ownerID, state); err != nil {
		return SendState{}, "", nil, nil, nil, err
	}
	return state, record.Content, record.ReplyTo, record.Template, record.Rich, nil
}
func (r *DynamoSendRepository) Fail(ctx context.Context, ownerID, id string, name SendStateName, message string, now time.Time) (SendState, error) {
	return r.transition(ctx, ownerID, id, name, message, now)
}

func (r *DynamoSendRepository) transition(ctx context.Context, ownerID, id string, stateName SendStateName, message string, now time.Time) (SendState, error) {
	state, record, err := r.read(ctx, id)
	if err != nil {
		return SendState{}, err
	}
	if record.OwnerID != ownerID {
		return SendState{}, ErrSendConflict
	}
	state.State, state.UpdatedAt = stateName, now
	state.Error, state.Reason, state.RetryAvailable, state.ResolutionRequired = nil, nil, nil, nil
	switch stateName {
	case SendFailedRetryable:
		value := true
		state.RetryAvailable = &value
		state.Error = &message
	case SendFailed:
		value := false
		state.RetryAvailable = &value
		state.Error = &message
	case SendUncertain:
		value := true
		state.ResolutionRequired = &value
		state.Reason = &message
	}
	values := map[string]types.AttributeValue{":state": &types.AttributeValueMemberS{Value: string(stateName)}, ":updated": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}}
	expression := "SET #state=:state, updatedAt=:updated REMOVE #error, reason, retryAvailable, resolutionRequired"
	names := map[string]string{"#state": "state", "#error": "error"}
	if stateName == SendFailedRetryable || stateName == SendFailed {
		values[":error"] = &types.AttributeValueMemberS{Value: message}
		values[":retry"] = &types.AttributeValueMemberBOOL{Value: stateName == SendFailedRetryable}
		expression = "SET #state=:state, updatedAt=:updated, #error=:error, retryAvailable=:retry REMOVE reason, resolutionRequired"
	}
	if stateName == SendUncertain {
		values[":reason"] = &types.AttributeValueMemberS{Value: message}
		values[":required"] = &types.AttributeValueMemberBOOL{Value: true}
		expression = "SET #state=:state, updatedAt=:updated, reason=:reason, resolutionRequired=:required REMOVE #error, retryAvailable"
	}
	_, err = r.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: &r.tableName, Key: snapshotKey("SEND#"+id, "META"), UpdateExpression: &expression, ExpressionAttributeNames: names, ExpressionAttributeValues: values})
	if err != nil {
		return SendState{}, fmt.Errorf("transition send: %w", err)
	}
	if err = r.putView(ctx, ownerID, state); err != nil {
		return SendState{}, err
	}
	return state, nil
}

func (r *DynamoSendRepository) Commit(ctx context.Context, ownerID, id, wamid string, now time.Time) (SendState, error) {
	state, record, err := r.read(ctx, id)
	if err != nil {
		return SendState{}, err
	}
	if record.OwnerID != ownerID {
		return SendState{}, ErrSendConflict
	}
	messageID, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return SendState{}, err
	}
	msgSK := "MSG#" + messageID.String()
	result := &SendResult{WhatsAppMessageID: wamid, MessageSK: msgSK, CreatedAt: now}
	retry := false
	state.State, state.UpdatedAt, state.Result, state.RetryAvailable = SendCommitted, now, result, &retry
	attachments := []MessageAttachment{}
	if state.Attachment != nil {
		attachments = append(attachments, MessageAttachment{FileName: state.Attachment.FileName, MIMEType: state.Attachment.MIMEType, Size: state.Attachment.Size})
	}
	actor := record.Actor
	if actor == "" {
		actor = MessageTypeTeam
	}
	message := Message{ID: messageID.String(), MessageSK: msgSK, Phone: state.Phone, Type: actor, Content: &record.Content, CreatedAt: now, WhatsAppMessageID: &wamid, DeliveryStatus: deliveryPointer(DeliveryStatusSent), Attachments: attachments, ReplyTo: record.ReplyTo, Reactions: []MessageReaction{}, Rich: record.Rich}
	messageItem, _ := attributevalue.MarshalMap(message)
	messageItem["pk"] = &types.AttributeValueMemberS{Value: "CONV#" + state.Phone}
	messageItem["sk"] = &types.AttributeValueMemberS{Value: msgSK}
	messageItem["expiresAt"] = &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(90 * 24 * time.Hour).Unix())}
	stateValues, _ := attributevalue.MarshalMap(state)
	_, err = r.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Put: &types.Put{TableName: &r.tableName, Item: messageItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}}, {Update: &types.Update{TableName: &r.tableName, Key: snapshotKey("SEND#"+id, "META"), UpdateExpression: aws.String("SET #state=:state, updatedAt=:updated, #result=:result, retryAvailable=:retry"), ExpressionAttributeNames: map[string]string{"#state": "state", "#result": "result"}, ExpressionAttributeValues: map[string]types.AttributeValue{":state": stateValues["state"], ":updated": stateValues["updatedAt"], ":result": stateValues["result"], ":retry": stateValues["retryAvailable"]}}}, {Delete: &types.Delete{TableName: &r.tableName, Key: snapshotKey("SEND_VIEW#"+ownerID+"#"+state.Phone, state.CreatedAt.Format(time.RFC3339Nano)+"#"+id)}}, {Put: &types.Put{TableName: &r.tableName, Item: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "WAMID#" + wamid}, "sk": &types.AttributeValueMemberS{Value: "META"}, "phone": &types.AttributeValueMemberS{Value: state.Phone}, "msgSk": &types.AttributeValueMemberS{Value: msgSK}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(90 * 24 * time.Hour).Unix())}}}}}})
	if err != nil {
		return SendState{}, fmt.Errorf("commit send: %w", err)
	}
	return state, nil
}

func (r *DynamoSendRepository) read(ctx context.Context, id string) (SendState, sendRecord, error) {
	out, err := r.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &r.tableName, Key: snapshotKey("SEND#"+id, "META"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return SendState{}, sendRecord{}, fmt.Errorf("read send: %w", err)
	}
	if len(out.Item) == 0 {
		return SendState{}, sendRecord{}, ErrSendNotFound
	}
	var record sendRecord
	if err = attributevalue.UnmarshalMap(out.Item, &record); err != nil {
		return SendState{}, sendRecord{}, fmt.Errorf("decode send: %w", err)
	}
	return record.SendState, record, nil
}
func (r *DynamoSendRepository) putView(ctx context.Context, owner string, state SendState) error {
	item, err := attributevalue.MarshalMap(state)
	if err != nil {
		return err
	}
	item["pk"] = &types.AttributeValueMemberS{Value: "SEND_VIEW#" + owner + "#" + state.Phone}
	item["sk"] = &types.AttributeValueMemberS{Value: state.CreatedAt.Format(time.RFC3339Nano) + "#" + state.ClientMessageID}
	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: &r.tableName, Item: item})
	if err != nil {
		return fmt.Errorf("write send view: %w", err)
	}
	return nil
}
func sanitizeSendText(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	r := []rune(value)
	if len(r) > 200 {
		r = r[:200]
	}
	return string(r)
}
func deliveryPointer(value DeliveryStatus) *DeliveryStatus { return &value }
