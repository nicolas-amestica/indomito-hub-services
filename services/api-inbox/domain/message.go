package domain

import "time"

// MessageType identifica el autor funcional de un mensaje.
type MessageType string

// DeliveryStatus representa el estado informado por WhatsApp.
type DeliveryStatus string

const (
	MessageTypeCustomer     MessageType    = "customer"
	MessageTypeAIAgent      MessageType    = "ai_agent"
	MessageTypeTeam         MessageType    = "team"
	DeliveryStatusSent      DeliveryStatus = "sent"
	DeliveryStatusDelivered DeliveryStatus = "delivered"
	DeliveryStatusRead      DeliveryStatus = "read"
	DeliveryStatusFailed    DeliveryStatus = "failed"
)

// MessageAttachment describe un adjunto visible en el historial.
type MessageAttachment struct {
	FileName    string  `json:"fileName" dynamodbav:"fileName"`
	MIMEType    string  `json:"mimeType" dynamodbav:"mimeType"`
	Size        int64   `json:"size" dynamodbav:"size"`
	DownloadURL *string `json:"downloadUrl" dynamodbav:"downloadUrl"`
	Bucket      string  `json:"-" dynamodbav:"bucket,omitempty"`
	Key         string  `json:"-" dynamodbav:"key,omitempty"`
}

// MessageReference preserves the message quoted by a contextual reply.
type MessageReference struct {
	ID                string      `json:"id" dynamodbav:"id"`
	Type              MessageType `json:"type" dynamodbav:"type"`
	Content           *string     `json:"content" dynamodbav:"content"`
	WhatsAppMessageID string      `json:"whatsappMessageId" dynamodbav:"whatsappMessageId"`
}

type MessageReaction struct {
	Emoji     string      `json:"emoji" dynamodbav:"emoji"`
	Actor     MessageType `json:"actor" dynamodbav:"actor"`
	UpdatedAt time.Time   `json:"updatedAt" dynamodbav:"updatedAt"`
}

// Message representa un mensaje persistido de una conversación.
type Message struct {
	ID                string               `json:"id" dynamodbav:"id"`
	MessageSK         string               `json:"msgSk" dynamodbav:"sk"`
	Phone             string               `json:"phone" dynamodbav:"phone"`
	Type              MessageType          `json:"type" dynamodbav:"type"`
	Content           *string              `json:"content" dynamodbav:"content"`
	CreatedAt         time.Time            `json:"createdAt" dynamodbav:"createdAt"`
	WhatsAppMessageID *string              `json:"whatsappMessageId" dynamodbav:"whatsappMessageId"`
	DeliveryStatus    *DeliveryStatus      `json:"deliveryStatus" dynamodbav:"deliveryStatus"`
	DeliveryError     *string              `json:"deliveryError" dynamodbav:"deliveryError"`
	Attachments       []MessageAttachment  `json:"attachments" dynamodbav:"attachments"`
	ReplyTo           *MessageReference    `json:"replyTo" dynamodbav:"replyTo,omitempty"`
	Reactions         []MessageReaction    `json:"reactions" dynamodbav:"reactions"`
	ReactionUpdatedAt map[string]time.Time `json:"-" dynamodbav:"reactionUpdatedAt,omitempty"`
	Rich              *MessageRichContent  `json:"rich,omitempty" dynamodbav:"rich,omitempty"`
	Referral          *MetaReferral        `json:"referral,omitempty" dynamodbav:"referral,omitempty"`
}
