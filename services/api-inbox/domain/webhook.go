package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/oklog/ulid/v2"
)

var ErrWebhookCapacity = errors.New("webhook persistence unavailable")

// InboundMetaMessage is the minimal verified provider event persisted for asynchronous processing.
type InboundMetaMessage struct {
	MessageID string
	Phone     string
	Type      string
	Timestamp string
	Text      string
	MediaID   string
	MIMEType  string
	SHA256    string
	FileName  string
}

type webhookDynamo interface {
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

// WebhookService authenticates and durably reserves inbound Meta messages.
type WebhookService struct {
	db               webhookDynamo
	table, appSecret string
	now              func() time.Time
}

func NewWebhookService(db webhookDynamo, table, appSecret string, now func() time.Time) (*WebhookService, error) {
	if db == nil || table == "" || len(appSecret) < 16 {
		return nil, errors.New("webhook service configuration is required")
	}
	if now == nil {
		now = time.Now
	}
	return &WebhookService{db: db, table: table, appSecret: appSecret, now: now}, nil
}

func (s *WebhookService) VerifySignature(raw []byte, signature string) bool {
	if len(signature) != 71 || !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	provided, err := hex.DecodeString(signature[7:])
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.appSecret))
	_, _ = mac.Write(raw)
	return hmac.Equal(provided, mac.Sum(nil))
}

func DecodeInboundMetaMessages(raw []byte) ([]InboundMetaMessage, error) {
	var envelope struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Messages []struct {
						ID, From, Type, Timestamp string
						Text                      struct {
							Body string `json:"body"`
						} `json:"text"`
						Audio    struct{ ID, MIMEType, SHA256 string }                    `json:"audio"`
						Image    struct{ ID, MIMEType, SHA256, Caption string }           `json:"image"`
						Video    struct{ ID, MIMEType, SHA256, Caption string }           `json:"video"`
						Document struct{ ID, MIMEType, SHA256, Filename, Caption string } `json:"document"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	result := make([]InboundMetaMessage, 0)
	for _, entry := range envelope.Entry {
		for _, change := range entry.Changes {
			for _, message := range change.Value.Messages {
				if message.ID == "" || message.From == "" {
					continue
				}
				mediaID, mimeType, checksum, fileName, text := "", "", "", "", message.Text.Body
				switch message.Type {
				case "audio":
					mediaID, mimeType, checksum = message.Audio.ID, message.Audio.MIMEType, message.Audio.SHA256
				case "image":
					mediaID, mimeType, checksum, text = message.Image.ID, message.Image.MIMEType, message.Image.SHA256, message.Image.Caption
				case "video":
					mediaID, mimeType, checksum, text = message.Video.ID, message.Video.MIMEType, message.Video.SHA256, message.Video.Caption
				case "document":
					mediaID, mimeType, checksum, fileName, text = message.Document.ID, message.Document.MIMEType, message.Document.SHA256, message.Document.Filename, message.Document.Caption
				}
				result = append(result, InboundMetaMessage{MessageID: message.ID, Phone: message.From, Type: message.Type, Timestamp: message.Timestamp, Text: text, MediaID: mediaID, MIMEType: mimeType, SHA256: checksum, FileName: fileName})
			}
		}
	}
	return result, nil
}

func (s *WebhookService) Reserve(ctx context.Context, message InboundMetaMessage) error {
	now := s.now().UTC()
	eventID := ulid.Make().String()
	meta := map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: "META_MSG#" + message.MessageID}, "sk": &types.AttributeValueMemberS{Value: "META"},
		"messageId": &types.AttributeValueMemberS{Value: message.MessageID}, "phone": &types.AttributeValueMemberS{Value: message.Phone},
		"messageType": &types.AttributeValueMemberS{Value: message.Type}, "providerTimestamp": &types.AttributeValueMemberS{Value: message.Timestamp},
		"text": &types.AttributeValueMemberS{Value: message.Text}, "state": &types.AttributeValueMemberS{Value: "reserved"},
		"createdAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(7 * 24 * time.Hour).Unix())},
	}
	for key, value := range map[string]string{"mediaId": message.MediaID, "mimeType": message.MIMEType, "mediaSha256": message.SHA256, "fileName": message.FileName} {
		if value != "" {
			meta[key] = &types.AttributeValueMemberS{Value: value}
		}
	}
	shard := conversationShard(message.MessageID)
	outbox := map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("WORK_DUE#INBOUND#%02X", shard)}, "sk": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano) + "#" + eventID},
		"eventId": &types.AttributeValueMemberS{Value: eventID}, "eventType": &types.AttributeValueMemberS{Value: "inbound.received"},
		"messageId": &types.AttributeValueMemberS{Value: message.MessageID}, "phone": &types.AttributeValueMemberS{Value: message.Phone},
		"state": &types.AttributeValueMemberS{Value: "pending"}, "nextAttemptAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		"expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(14 * 24 * time.Hour).Unix())},
	}
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: &s.table, Item: meta, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: &s.table, Item: outbox, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}})
	if err != nil {
		var canceled *types.TransactionCanceledException
		if errors.As(err, &canceled) {
			return nil
		}
		return fmt.Errorf("reserve inbound webhook: %w", err)
	}
	return nil
}
