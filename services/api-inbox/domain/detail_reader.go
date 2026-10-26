package domain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/oklog/ulid/v2"
)

// NonTerminalSend es la proyección mínima necesaria para recuperar envíos pendientes.
type NonTerminalSend struct {
	ClientMessageID    string          `json:"clientMessageId" dynamodbav:"clientMessageId"`
	Phone              string          `json:"phone" dynamodbav:"phone"`
	State              string          `json:"state" dynamodbav:"state"`
	CreatedAt          time.Time       `json:"createdAt" dynamodbav:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt" dynamodbav:"updatedAt"`
	RetryCount         int             `json:"retryCount" dynamodbav:"retryCount"`
	Preview            *string         `json:"preview" dynamodbav:"preview"`
	Attachment         *SendAttachment `json:"attachment" dynamodbav:"attachment"`
	WhatsAppMessageID  *string         `json:"whatsappMessageId,omitempty" dynamodbav:"whatsappMessageId,omitempty"`
	ResolutionRequired *bool           `json:"resolutionRequired,omitempty" dynamodbav:"resolutionRequired,omitempty"`
	Reason             *string         `json:"reason,omitempty" dynamodbav:"reason,omitempty"`
	RetryAvailable     *bool           `json:"retryAvailable,omitempty" dynamodbav:"retryAvailable,omitempty"`
	Error              *string         `json:"error,omitempty" dynamodbav:"error,omitempty"`
}

// ReadConversationDetailDelta carga mensajes posteriores al instante solicitado.
func ReadConversationDetailDelta(ctx context.Context, reader detailReader, tableName, ownerID, phone string, since time.Time) (ConversationDetailSnapshot, error) {
	sequence, err := readDetailSequence(ctx, reader, tableName, phone)
	if err != nil {
		return ConversationDetailSnapshot{}, err
	}
	conversation, err := readConversation(ctx, reader, tableName, phone)
	if err != nil {
		return ConversationDetailSnapshot{}, err
	}
	messages, err := readMessagesSince(ctx, reader, tableName, phone, since)
	if err != nil {
		return ConversationDetailSnapshot{}, err
	}
	sends, err := readNonTerminalSends(ctx, reader, tableName, ownerID, phone)
	if err != nil {
		return ConversationDetailSnapshot{}, err
	}
	return ConversationDetailSnapshot{Conversation: conversation, Messages: messages, NonTerminalSends: sends, Sequence: sequence}, nil
}

// ConversationDetailSnapshot contiene una lectura coherente respecto del watermark inicial.
type ConversationDetailSnapshot struct {
	Conversation     ConversationSummary
	Messages         []Message
	NonTerminalSends []NonTerminalSend
	Sequence         uint64
	HasMore          bool
	OldestMessageSK  string
}

type detailReader interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// ReadConversationDetail captura primero la secuencia y luego carga una página completa.
func ReadConversationDetail(ctx context.Context, reader detailReader, tableName, ownerID, phone, before string, pageSize int) (ConversationDetailSnapshot, error) {
	if pageSize < 1 || pageSize > 50 {
		return ConversationDetailSnapshot{}, errors.New("detail page size out of range")
	}
	sequence, err := readDetailSequence(ctx, reader, tableName, phone)
	if err != nil {
		return ConversationDetailSnapshot{}, err
	}
	conversation, err := readConversation(ctx, reader, tableName, phone)
	if err != nil {
		return ConversationDetailSnapshot{}, err
	}
	messages, hasMore, oldest, err := readMessages(ctx, reader, tableName, phone, before, pageSize)
	if err != nil {
		return ConversationDetailSnapshot{}, err
	}
	sends, err := readNonTerminalSends(ctx, reader, tableName, ownerID, phone)
	if err != nil {
		return ConversationDetailSnapshot{}, err
	}
	return ConversationDetailSnapshot{Conversation: conversation, Messages: messages, NonTerminalSends: sends, Sequence: sequence, HasMore: hasMore, OldestMessageSK: oldest}, nil
}

func readDetailSequence(ctx context.Context, reader detailReader, tableName, phone string) (uint64, error) {
	output, err := reader.GetItem(ctx, &dynamodb.GetItemInput{TableName: &tableName, ConsistentRead: boolPointer(true), Key: snapshotKey("SEQ#DETAIL#"+phone, "META"), ProjectionExpression: stringPointer("#sequence"), ExpressionAttributeNames: map[string]string{"#sequence": "sequence"}})
	if err != nil {
		return 0, fmt.Errorf("read detail sequence: %w", err)
	}
	if len(output.Item) == 0 {
		return 0, nil
	}
	var sequence listSequence
	if err := attributevalue.UnmarshalMap(output.Item, &sequence); err != nil {
		return 0, fmt.Errorf("decode detail sequence: %w", err)
	}
	return sequence.Sequence, nil
}

func readConversation(ctx context.Context, reader detailReader, tableName, phone string) (ConversationSummary, error) {
	output, err := reader.GetItem(ctx, &dynamodb.GetItemInput{TableName: &tableName, ConsistentRead: boolPointer(true), Key: snapshotKey("CONV#"+phone, "META")})
	if err != nil {
		return ConversationSummary{}, fmt.Errorf("read conversation: %w", err)
	}
	if len(output.Item) == 0 {
		return ConversationSummary{ID: phone, Phone: phone, Alias: phone, Channel: "whatsapp", Status: ConversationStatusBot}, nil
	}
	var conversation ConversationSummary
	if err := attributevalue.UnmarshalMap(output.Item, &conversation); err != nil {
		return ConversationSummary{}, fmt.Errorf("decode conversation: %w", err)
	}
	return conversation, nil
}

func readMessages(ctx context.Context, reader detailReader, tableName, phone, before string, pageSize int) ([]Message, bool, string, error) {
	values := map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "CONV#" + phone}, ":message": &types.AttributeValueMemberS{Value: "MSG#"}}
	condition := "pk = :pk AND begins_with(sk, :message)"
	var startKey map[string]types.AttributeValue
	if before != "" {
		startKey = snapshotKey("CONV#"+phone, before)
	}
	limit := int32(pageSize + 1)
	output, err := reader.Query(ctx, &dynamodb.QueryInput{TableName: &tableName, ConsistentRead: boolPointer(true), KeyConditionExpression: &condition, ExpressionAttributeValues: values, ExclusiveStartKey: startKey, ScanIndexForward: boolPointer(false), Limit: &limit})
	if err != nil {
		return nil, false, "", fmt.Errorf("read conversation messages: %w", err)
	}
	var messages []Message
	if err := attributevalue.UnmarshalListOfMaps(output.Items, &messages); err != nil {
		return nil, false, "", fmt.Errorf("decode conversation messages: %w", err)
	}
	normalizeMessages(messages)
	hasMore := len(messages) > pageSize
	if hasMore {
		messages = messages[:pageSize]
	}
	slices.Reverse(messages)
	oldest := ""
	if len(messages) > 0 {
		oldest = messages[0].MessageSK
	}
	return messages, hasMore, oldest, nil
}

func readNonTerminalSends(ctx context.Context, reader detailReader, tableName, ownerID, phone string) ([]NonTerminalSend, error) {
	partition := "SEND_VIEW#" + ownerID + "#" + phone
	output, err := reader.Query(ctx, &dynamodb.QueryInput{TableName: &tableName, ConsistentRead: boolPointer(true), KeyConditionExpression: stringPointer("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: partition}}, ScanIndexForward: boolPointer(true)})
	if err != nil {
		return nil, fmt.Errorf("read non-terminal sends: %w", err)
	}
	var sends []NonTerminalSend
	if err := attributevalue.UnmarshalListOfMaps(output.Items, &sends); err != nil {
		return nil, fmt.Errorf("decode non-terminal sends: %w", err)
	}
	sends = slices.DeleteFunc(sends, func(send NonTerminalSend) bool {
		switch SendStateName(send.State) {
		case SendReserved, SendProviderPending, SendRecovering, SendUncertain, SendFailedRetryable:
			return false
		default:
			return true
		}
	})
	return sends, nil
}

func readMessagesSince(ctx context.Context, reader detailReader, tableName, phone string, since time.Time) ([]Message, error) {
	boundary, err := ulid.New(ulid.Timestamp(since.UTC()), bytes.NewReader(make([]byte, 10)))
	if err != nil {
		return nil, fmt.Errorf("build conversation delta boundary: %w", err)
	}
	values := map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "CONV#" + phone}, ":after": &types.AttributeValueMemberS{Value: "MSG#" + boundary.String()}}
	condition := "pk = :pk AND sk > :after"
	var messages []Message
	var startKey map[string]types.AttributeValue
	for {
		output, err := reader.Query(ctx, &dynamodb.QueryInput{TableName: &tableName, ConsistentRead: boolPointer(true), KeyConditionExpression: &condition, ExpressionAttributeValues: values, ExclusiveStartKey: startKey, ScanIndexForward: boolPointer(true)})
		if err != nil {
			return nil, fmt.Errorf("read conversation delta: %w", err)
		}
		var page []Message
		if err := attributevalue.UnmarshalListOfMaps(output.Items, &page); err != nil {
			return nil, fmt.Errorf("decode conversation delta: %w", err)
		}
		normalizeMessages(page)
		messages = append(messages, page...)
		if len(output.LastEvaluatedKey) == 0 {
			return messages, nil
		}
		startKey = output.LastEvaluatedKey
	}
}

func normalizeMessages(messages []Message) {
	for index := range messages {
		if messages[index].Attachments == nil {
			messages[index].Attachments = []MessageAttachment{}
		}
		if messages[index].Reactions == nil {
			messages[index].Reactions = []MessageReaction{}
		}
	}
}
