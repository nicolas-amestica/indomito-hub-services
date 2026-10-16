package domain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/oklog/ulid/v2"
)

type inboundDynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

type inboundRecord struct {
	MessageID         string `dynamodbav:"messageId"`
	Phone             string `dynamodbav:"phone"`
	MessageType       string `dynamodbav:"messageType"`
	ProviderTimestamp string `dynamodbav:"providerTimestamp"`
	Text              string `dynamodbav:"text"`
	State             string `dynamodbav:"state"`
	MediaID           string `dynamodbav:"mediaId"`
	MIMEType          string `dynamodbav:"mimeType"`
	MediaSHA256       string `dynamodbav:"mediaSha256"`
	FileName          string `dynamodbav:"fileName"`
}

type InboundMediaStore interface {
	Materialize(context.Context, string, string, string, string, string, string) (MessageAttachment, error)
}

// InboundProcessor materializes a verified Meta message into all visible Inbox projections.
type InboundProcessor struct {
	client    inboundDynamo
	tableName string
	now       func() time.Time
	media     InboundMediaStore
}

func NewInboundProcessorWithMedia(client inboundDynamo, tableName string, media InboundMediaStore, now func() time.Time) (*InboundProcessor, error) {
	p, err := NewInboundProcessor(client, tableName, now)
	if err == nil {
		p.media = media
	}
	return p, err
}

// NewInboundProcessor creates an idempotent transactional inbound processor.
func NewInboundProcessor(client inboundDynamo, tableName string, now func() time.Time) (*InboundProcessor, error) {
	if client == nil || tableName == "" {
		return nil, errors.New("inbound processor configuration is required")
	}
	if now == nil {
		now = time.Now
	}
	return &InboundProcessor{client: client, tableName: tableName, now: now}, nil
}

// Process persists the customer message, list/search versions and realtime event exactly once.
func (p *InboundProcessor) Process(ctx context.Context, messageID string) error {
	if strings.TrimSpace(messageID) == "" {
		return errors.New("inbound message id is required")
	}
	for attempt := 0; attempt < mutationMaxAttempts; attempt++ {
		record, processed, err := p.readRecord(ctx, messageID)
		if err != nil || processed {
			return err
		}
		summary, listSequence, detailSequence, found, err := p.readConversationState(ctx, record.Phone)
		if err != nil {
			return err
		}
		occurredAt := inboundTime(record.ProviderTimestamp, p.now().UTC())
		attachments := []MessageAttachment{}
		if record.MediaID != "" {
			if p.media == nil {
				return errors.New("inbound media store is required")
			}
			attachment, mediaErr := p.media.Materialize(ctx, record.Phone, record.MessageID, record.MediaID, record.MIMEType, record.FileName, record.MediaSHA256)
			if mediaErr != nil {
				return mediaErr
			}
			attachments = append(attachments, attachment)
		}
		preview := sanitizeSendText(record.Text)
		if preview == "" {
			preview = "[" + record.MessageType + "]"
		}
		if !found {
			summary = ConversationSummary{ID: record.Phone, Phone: record.Phone, Alias: record.Phone, Channel: "whatsapp", Status: ConversationStatusBot, CreatedAt: &occurredAt}
		}
		summary.UnreadCount++
		summary.LastMessageAt, summary.UpdatedAt, summary.LastMessagePreview = &occurredAt, &occurredAt, &preview
		summary.Generation++
		mutation := &ConversationMutationService{client: p.client, tableName: p.tableName, now: p.now, newID: func(at time.Time) (string, error) { return ulid.Make().String(), nil }}
		writes, err := mutation.visibleWrites(summary, "upsert", listSequence, detailSequence, "message.created", occurredAt)
		if err != nil {
			return err
		}
		if !found {
			setNewConversationCondition(writes, record.Phone)
			writes = append(writes, types.TransactWriteItem{Update: &types.Update{TableName: &p.tableName, Key: snapshotKey("CONV_COUNT", "META"), UpdateExpression: aws.String("ADD activeCount :one"), ExpressionAttributeValues: map[string]types.AttributeValue{":one": &types.AttributeValueMemberN{Value: "1"}}}})
		}
		entropy := sha256.Sum256([]byte(messageID))
		messageULID, err := ulid.New(ulid.Timestamp(occurredAt), bytes.NewReader(entropy[:10]))
		if err != nil {
			return fmt.Errorf("create inbound message id: %w", err)
		}
		content := record.Text
		message := Message{ID: messageULID.String(), MessageSK: "MSG#" + messageULID.String(), Phone: record.Phone, Type: MessageTypeCustomer, Content: &content, CreatedAt: occurredAt, WhatsAppMessageID: &record.MessageID, Attachments: attachments}
		messageItem, err := attributevalue.MarshalMap(message)
		if err != nil {
			return fmt.Errorf("encode inbound message: %w", err)
		}
		messageItem["pk"] = &types.AttributeValueMemberS{Value: "CONV#" + record.Phone}
		messageItem["expiresAt"] = &types.AttributeValueMemberN{Value: fmt.Sprint(p.now().UTC().Add(90 * 24 * time.Hour).Unix())}
		writes = append(writes,
			types.TransactWriteItem{Put: &types.Put{TableName: &p.tableName, Item: messageItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
			types.TransactWriteItem{Update: &types.Update{TableName: &p.tableName, Key: snapshotKey("META_MSG#"+messageID, "META"), UpdateExpression: aws.String("SET #state=:processed, processedAt=:now"), ConditionExpression: aws.String("#state=:reserved"), ExpressionAttributeNames: map[string]string{"#state": "state"}, ExpressionAttributeValues: map[string]types.AttributeValue{":processed": &types.AttributeValueMemberS{Value: "processed"}, ":reserved": &types.AttributeValueMemberS{Value: "reserved"}, ":now": &types.AttributeValueMemberS{Value: p.now().UTC().Format(time.RFC3339Nano)}}}},
			types.TransactWriteItem{Put: &types.Put{TableName: &p.tableName, Item: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("WORK_DUE#AGENT#%02X", conversationShard(record.Phone))}, "sk": &types.AttributeValueMemberS{Value: p.now().UTC().Format(time.RFC3339Nano) + "#" + messageID}, "eventId": &types.AttributeValueMemberS{Value: "agent-" + messageID}, "eventType": &types.AttributeValueMemberS{Value: "agent.requested"}, "messageId": &types.AttributeValueMemberS{Value: messageID}, "phone": &types.AttributeValueMemberS{Value: record.Phone}, "state": &types.AttributeValueMemberS{Value: "pending"}, "nextAttemptAt": &types.AttributeValueMemberS{Value: p.now().UTC().Format(time.RFC3339Nano)}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(p.now().UTC().Add(14 * 24 * time.Hour).Unix())}}, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		)
		_, err = p.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
		if err == nil {
			return nil
		}
		if !isTransactionConflict(err) {
			return fmt.Errorf("materialize inbound message: %w", err)
		}
	}
	return errors.New("materialize inbound message: concurrent update limit reached")
}

func (p *InboundProcessor) readRecord(ctx context.Context, messageID string) (inboundRecord, bool, error) {
	output, err := p.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &p.tableName, Key: snapshotKey("META_MSG#"+messageID, "META"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return inboundRecord{}, false, fmt.Errorf("read inbound reservation: %w", err)
	}
	if len(output.Item) == 0 {
		return inboundRecord{}, false, errors.New("inbound reservation not found")
	}
	var record inboundRecord
	if err = attributevalue.UnmarshalMap(output.Item, &record); err != nil {
		return inboundRecord{}, false, fmt.Errorf("decode inbound reservation: %w", err)
	}
	return record, record.State == "processed", nil
}

func (p *InboundProcessor) readConversationState(ctx context.Context, phone string) (ConversationSummary, uint64, uint64, bool, error) {
	mutation := &ConversationMutationService{client: p.client, tableName: p.tableName}
	return mutation.readState(ctx, phone)
}

func inboundTime(raw string, fallback time.Time) time.Time {
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Unix(seconds, 0).UTC()
}

func setNewConversationCondition(writes []types.TransactWriteItem, phone string) {
	for _, write := range writes {
		if write.Put == nil {
			continue
		}
		pk, ok := write.Put.Item["pk"].(*types.AttributeValueMemberS)
		if ok && pk.Value == "CONV#"+phone {
			write.Put.ConditionExpression = aws.String("attribute_not_exists(pk)")
			write.Put.ExpressionAttributeValues = nil
			return
		}
	}
}
