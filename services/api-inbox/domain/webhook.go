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
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
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
	Rich      *MessageRichContent
}

// InboundMetaStatus is a delivery receipt emitted by WhatsApp Cloud API.
type InboundMetaStatus struct {
	WhatsAppMessageID string
	Status            DeliveryStatus
	Timestamp         string
	Error             string
}

type pendingMetaStatus struct {
	PK        string         `dynamodbav:"pk"`
	SK        string         `dynamodbav:"sk"`
	Status    DeliveryStatus `dynamodbav:"status"`
	Timestamp string         `dynamodbav:"providerTimestamp"`
	Error     string         `dynamodbav:"error,omitempty"`
	ExpiresAt int64          `dynamodbav:"expiresAt"`
}

type webhookDynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

// DecodeInboundMetaStatuses extracts supported delivery receipts from a verified webhook body.
func DecodeInboundMetaStatuses(raw []byte) ([]InboundMetaStatus, error) {
	var envelope struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Statuses []struct {
						ID        string `json:"id"`
						Status    string `json:"status"`
						Timestamp string `json:"timestamp"`
						Errors    []struct {
							Code    int    `json:"code"`
							Title   string `json:"title"`
							Message string `json:"message"`
						} `json:"errors"`
					} `json:"statuses"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	result := make([]InboundMetaStatus, 0)
	for _, entry := range envelope.Entry {
		for _, change := range entry.Changes {
			for _, status := range change.Value.Statuses {
				mapped := DeliveryStatus(status.Status)
				if status.ID == "" || (mapped != DeliveryStatusSent && mapped != DeliveryStatusDelivered && mapped != DeliveryStatusRead && mapped != DeliveryStatusFailed) {
					continue
				}
				errorText := ""
				if len(status.Errors) > 0 {
					errorText = strings.TrimSpace(status.Errors[0].Message)
					if errorText == "" {
						errorText = strings.TrimSpace(status.Errors[0].Title)
					}
					if status.Errors[0].Code != 0 {
						errorText = fmt.Sprintf("%d: %s", status.Errors[0].Code, errorText)
					}
				}
				result = append(result, InboundMetaStatus{WhatsAppMessageID: status.ID, Status: mapped, Timestamp: status.Timestamp, Error: normalizeDerivationText(errorText, 500)})
			}
		}
	}
	return result, nil
}

// ApplyStatus updates a known outbound message monotonically and publishes the change.
func (s *WebhookService) ApplyStatus(ctx context.Context, status InboundMetaStatus) error {
	if status.Status == DeliveryStatusSent || status.WhatsAppMessageID == "" {
		return nil
	}
	phone, msgSK := "", ""
	for attempt := 0; attempt < 2; attempt++ {
		index, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: snapshotKey("WAMID#"+status.WhatsAppMessageID, "META"), ConsistentRead: aws.Bool(true)})
		if err != nil {
			return fmt.Errorf("read WhatsApp delivery index: %w", err)
		}
		phone, msgSK = attributeString(index.Item, "phone"), attributeString(index.Item, "msgSk")
		if phone != "" && msgSK != "" {
			break
		}
		if attempt == 0 {
			s.sleep(400 * time.Millisecond)
		}
	}
	if phone == "" || msgSK == "" {
		if err := s.persistPendingStatus(ctx, status); err != nil {
			return err
		}
		index, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: snapshotKey("WAMID#"+status.WhatsAppMessageID, "META"), ConsistentRead: aws.Bool(true)})
		if err != nil {
			return fmt.Errorf("recheck WhatsApp delivery index: %w", err)
		}
		phone, msgSK = attributeString(index.Item, "phone"), attributeString(index.Item, "msgSk")
		if phone == "" || msgSK == "" {
			return nil
		}
	}
	for attempt := 0; attempt < mutationMaxAttempts; attempt++ {
		messageOut, readErr := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: snapshotKey("CONV#"+phone, msgSK), ConsistentRead: aws.Bool(true)})
		if readErr != nil {
			return fmt.Errorf("read WhatsApp delivery message: %w", readErr)
		}
		if len(messageOut.Item) == 0 {
			return nil
		}
		var message Message
		if readErr = attributevalue.UnmarshalMap(messageOut.Item, &message); readErr != nil {
			return fmt.Errorf("decode WhatsApp delivery message: %w", readErr)
		}
		if !shouldApplyDeliveryStatus(message.DeliveryStatus, status.Status) {
			return nil
		}
		sequence, sequenceErr := readSequenceItem(ctx, s.db, s.table, "SEQ#DETAIL#"+phone)
		if sequenceErr != nil {
			return sequenceErr
		}
		now := s.now().UTC()
		eventID := ulid.Make().String()
		nextSequence := sequence + 1
		values := map[string]types.AttributeValue{
			":nextStatus": &types.AttributeValueMemberS{Value: string(status.Status)},
		}
		condition := deliveryCondition(status.Status)
		switch status.Status {
		case DeliveryStatusDelivered:
			values[":sent"] = &types.AttributeValueMemberS{Value: string(DeliveryStatusSent)}
		case DeliveryStatusRead:
			values[":sent"] = &types.AttributeValueMemberS{Value: string(DeliveryStatusSent)}
			values[":delivered"] = &types.AttributeValueMemberS{Value: string(DeliveryStatusDelivered)}
		case DeliveryStatusFailed:
			values[":read"] = &types.AttributeValueMemberS{Value: string(DeliveryStatusRead)}
			values[":failed"] = &types.AttributeValueMemberS{Value: string(DeliveryStatusFailed)}
		}
		updateExpression := "SET #deliveryStatus=:nextStatus REMOVE deliveryError"
		if status.Status == DeliveryStatusFailed && status.Error != "" {
			values[":deliveryError"] = &types.AttributeValueMemberS{Value: status.Error}
			updateExpression = "SET #deliveryStatus=:nextStatus, deliveryError=:deliveryError"
		}
		payload, _ := attributevalue.MarshalMap(map[string]any{"phone": phone, "msgSk": msgSK, "whatsappMessageId": status.WhatsAppMessageID, "deliveryStatus": status.Status, "deliveryError": nullableString(status.Error)})
		outbox := map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("WORK_DUE#OUTBOX#%02X", conversationShard(phone))}, "sk": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano) + "#" + eventID},
			"eventId": &types.AttributeValueMemberS{Value: eventID}, "eventType": &types.AttributeValueMemberS{Value: "message.delivery.updated"}, "phone": &types.AttributeValueMemberS{Value: phone},
			"stream": &types.AttributeValueMemberS{Value: "detail:" + phone}, "sequence": &types.AttributeValueMemberN{Value: fmt.Sprint(nextSequence)}, "payload": &types.AttributeValueMemberM{Value: payload},
			"state": &types.AttributeValueMemberS{Value: "pending"}, "nextAttemptAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(14 * 24 * time.Hour).Unix())},
		}
		_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
			sequenceUpdate(s.table, "SEQ#DETAIL#"+phone, sequence, nextSequence),
			{Update: &types.Update{TableName: &s.table, Key: snapshotKey("CONV#"+phone, msgSK), UpdateExpression: aws.String(updateExpression), ConditionExpression: aws.String(condition), ExpressionAttributeNames: map[string]string{"#deliveryStatus": "deliveryStatus"}, ExpressionAttributeValues: values}},
			{Put: &types.Put{TableName: &s.table, Item: outbox, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
			{Delete: &types.Delete{TableName: &s.table, Key: snapshotKey("STATUS_PENDING#"+status.WhatsAppMessageID, "META")}},
		}})
		if err == nil {
			return nil
		}
		if !isTransactionConflict(err) {
			return fmt.Errorf("apply WhatsApp delivery status: %w", err)
		}
	}
	return ErrWebhookCapacity
}

func (s *WebhookService) persistPendingStatus(ctx context.Context, status InboundMetaStatus) error {
	now := s.now().UTC()
	record, err := attributevalue.MarshalMap(pendingMetaStatus{PK: "STATUS_PENDING#" + status.WhatsAppMessageID, SK: "META", Status: status.Status, Timestamp: status.Timestamp, Error: status.Error, ExpiresAt: now.Add(24 * time.Hour).Unix()})
	if err != nil {
		return fmt.Errorf("encode pending WhatsApp delivery status: %w", err)
	}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Put: &types.Put{TableName: &s.table, Item: record}}}})
	if err != nil {
		return fmt.Errorf("persist pending WhatsApp delivery status: %w", err)
	}
	return nil
}

func attributeString(item map[string]types.AttributeValue, key string) string {
	value, _ := item[key].(*types.AttributeValueMemberS)
	if value == nil {
		return ""
	}
	return value.Value
}

func shouldApplyDeliveryStatus(current *DeliveryStatus, next DeliveryStatus) bool {
	if current == nil {
		return true
	}
	if *current == DeliveryStatusFailed || *current == DeliveryStatusRead {
		return false
	}
	if next == DeliveryStatusFailed {
		return true
	}
	rank := map[DeliveryStatus]int{DeliveryStatusSent: 0, DeliveryStatusDelivered: 1, DeliveryStatusRead: 2}
	return rank[next] > rank[*current]
}

func deliveryCondition(next DeliveryStatus) string {
	if next == DeliveryStatusDelivered {
		return "attribute_not_exists(#deliveryStatus) OR #deliveryStatus=:sent"
	}
	if next == DeliveryStatusRead {
		return "attribute_not_exists(#deliveryStatus) OR #deliveryStatus=:sent OR #deliveryStatus=:delivered"
	}
	return "attribute_not_exists(#deliveryStatus) OR (#deliveryStatus<>:failed AND #deliveryStatus<>:read)"
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// WebhookService authenticates and durably reserves inbound Meta messages.
type WebhookService struct {
	db               webhookDynamo
	table, appSecret string
	now              func() time.Time
	sleep            func(time.Duration)
}

func NewWebhookService(db webhookDynamo, table, appSecret string, now func() time.Time) (*WebhookService, error) {
	if db == nil || table == "" || len(appSecret) < 16 {
		return nil, errors.New("webhook service configuration is required")
	}
	if now == nil {
		now = time.Now
	}
	return &WebhookService{db: db, table: table, appSecret: appSecret, now: now, sleep: time.Sleep}, nil
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
						Sticker  struct{ ID, MIMEType, SHA256 string }                    `json:"sticker"`
						Location struct {
							Latitude  float64 `json:"latitude"`
							Longitude float64 `json:"longitude"`
							Name      string  `json:"name"`
							Address   string  `json:"address"`
						} `json:"location"`
						Contacts []struct {
							Name struct {
								FormattedName string `json:"formatted_name"`
							} `json:"name"`
							Phones []struct {
								Phone string `json:"phone"`
							} `json:"phones"`
							Emails []struct {
								Email string `json:"email"`
							} `json:"emails"`
							Org struct {
								Company string `json:"company"`
							} `json:"org"`
						} `json:"contacts"`
						Interactive struct {
							Type        string                                  `json:"type"`
							ButtonReply struct{ ID, Title string }              `json:"button_reply"`
							ListReply   struct{ ID, Title, Description string } `json:"list_reply"`
						} `json:"interactive"`
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
				if message.Type == "reaction" {
					continue
				}
				mediaID, mimeType, checksum, fileName, text := "", "", "", "", message.Text.Body
				var rich *MessageRichContent
				switch message.Type {
				case "text":
					// The text body was decoded above.
				case "audio":
					mediaID, mimeType, checksum = message.Audio.ID, message.Audio.MIMEType, message.Audio.SHA256
				case "image":
					mediaID, mimeType, checksum, text = message.Image.ID, message.Image.MIMEType, message.Image.SHA256, message.Image.Caption
				case "video":
					mediaID, mimeType, checksum, text = message.Video.ID, message.Video.MIMEType, message.Video.SHA256, message.Video.Caption
				case "document":
					mediaID, mimeType, checksum, fileName, text = message.Document.ID, message.Document.MIMEType, message.Document.SHA256, message.Document.Filename, message.Document.Caption
				case "sticker":
					mediaID, mimeType, checksum = message.Sticker.ID, message.Sticker.MIMEType, message.Sticker.SHA256
					rich = &MessageRichContent{Kind: "sticker"}
				case "location":
					rich = &MessageRichContent{Kind: "location", Location: &MessageLocation{Latitude: message.Location.Latitude, Longitude: message.Location.Longitude, Name: message.Location.Name, Address: message.Location.Address}}
				case "contacts":
					contacts := make([]MessageContact, 0, len(message.Contacts))
					for _, contact := range message.Contacts {
						phone, email := "", ""
						if len(contact.Phones) > 0 {
							phone = contact.Phones[0].Phone
						}
						if len(contact.Emails) > 0 {
							email = contact.Emails[0].Email
						}
						contacts = append(contacts, MessageContact{Name: contact.Name.FormattedName, Phone: phone, Email: email, Company: contact.Org.Company})
					}
					rich = &MessageRichContent{Kind: "contacts", Contacts: contacts}
				case "interactive":
					reply := MessageInteractiveReply{Kind: message.Interactive.Type}
					if message.Interactive.Type == "button_reply" {
						reply.ID, reply.Title = message.Interactive.ButtonReply.ID, message.Interactive.ButtonReply.Title
					} else {
						reply.ID, reply.Title, reply.Description = message.Interactive.ListReply.ID, message.Interactive.ListReply.Title, message.Interactive.ListReply.Description
					}
					text = reply.Title
					rich = &MessageRichContent{Kind: "interactive_reply", InteractiveReply: &reply}
				default:
					kind := strings.TrimSpace(message.Type)
					if kind == "" {
						kind = "desconocido"
					}
					text = "[Contenido de WhatsApp no compatible: " + kind + "]"
				}
				result = append(result, InboundMetaMessage{MessageID: message.ID, Phone: message.From, Type: message.Type, Timestamp: message.Timestamp, Text: text, MediaID: mediaID, MIMEType: mimeType, SHA256: checksum, FileName: fileName, Rich: rich})
			}
		}
	}
	return result, nil
}

func DecodeInboundMetaReactions(raw []byte) ([]InboundMetaReaction, error) {
	var envelope struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Messages []struct {
						ID, From, Type, Timestamp string
						Reaction                  struct {
							MessageID string `json:"message_id"`
							Emoji     string `json:"emoji"`
						} `json:"reaction"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	result := []InboundMetaReaction{}
	for _, entry := range envelope.Entry {
		for _, change := range entry.Changes {
			for _, message := range change.Value.Messages {
				if message.Type == "reaction" && message.From != "" && message.Reaction.MessageID != "" {
					result = append(result, InboundMetaReaction{Phone: message.From, MessageID: message.Reaction.MessageID, Emoji: message.Reaction.Emoji, EventID: message.ID, Timestamp: message.Timestamp})
				}
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
	if message.Rich != nil {
		encoded, marshalErr := attributevalue.Marshal(message.Rich)
		if marshalErr != nil {
			return fmt.Errorf("encode rich inbound message: %w", marshalErr)
		}
		meta["rich"] = encoded
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
