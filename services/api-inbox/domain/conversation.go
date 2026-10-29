// Package domain contiene los contratos internos del servicio Inbox.
package domain

import (
	"errors"
	"time"
)

var ErrConversationNotFound = errors.New("conversation not found")

// ConversationStatus identifica quién controla actualmente la conversación.
type ConversationStatus string

const (
	// ConversationStatusBot indica que el agente automático mantiene el control.
	ConversationStatusBot ConversationStatus = "bot"
	// ConversationStatusDerived indica que la conversación fue derivada al equipo.
	ConversationStatusDerived ConversationStatus = "derived"
)

// ConversationSummary representa una fila estable de la bandeja.
type ConversationSummary struct {
	ID                     string             `json:"id" dynamodbav:"id"`
	Phone                  string             `json:"phone" dynamodbav:"phone"`
	Alias                  string             `json:"alias" dynamodbav:"alias"`
	Channel                string             `json:"channel" dynamodbav:"channel"`
	Status                 ConversationStatus `json:"status" dynamodbav:"status"`
	Reason                 *string            `json:"reason" dynamodbav:"reason"`
	ReasonLabel            *string            `json:"reasonLabel" dynamodbav:"reasonLabel"`
	AgentProcessing        bool               `json:"agentProcessing" dynamodbav:"agentProcessing"`
	AgentProcessingID      string             `json:"-" dynamodbav:"agentProcessingId,omitempty"`
	AgentProcessingUntil   *time.Time         `json:"agentProcessingUntil" dynamodbav:"agentProcessingUntil,omitempty"`
	UnreadCount            int                `json:"unreadCount" dynamodbav:"unreadCount"`
	LastMessagePreview     *string            `json:"lastMessagePreview" dynamodbav:"lastMessagePreview"`
	LastMessageAt          *time.Time         `json:"lastMessageAt" dynamodbav:"lastMessageAt"`
	ServiceWindowExpiresAt *time.Time         `json:"serviceWindowExpiresAt" dynamodbav:"serviceWindowExpiresAt"`
	CreatedAt              *time.Time         `json:"createdAt" dynamodbav:"createdAt"`
	UpdatedAt              *time.Time         `json:"updatedAt" dynamodbav:"updatedAt"`
	Generation             int                `json:"generation" dynamodbav:"generation"`
	Referral               *MetaReferral      `json:"referral,omitempty" dynamodbav:"referral,omitempty"`
}

// PageInfo describe una página materializada de la bandeja.
type PageInfo struct {
	Page       int `json:"page"`
	PageSize   int `json:"pageSize"`
	TotalItems int `json:"totalItems"`
	TotalPages int `json:"totalPages"`
}

// ListWatermark fija la secuencia observada antes de materializar una lista.
type ListWatermark struct {
	Stream   string `json:"stream"`
	Sequence uint64 `json:"sequence"`
}

// ConversationListResponse es la respuesta contractual de GET /conversaciones.
type ConversationListResponse struct {
	Data        []ConversationSummary `json:"data"`
	PageInfo    PageInfo              `json:"pageInfo"`
	NextCursor  *string               `json:"nextCursor"`
	UnreadTotal int                   `json:"unreadTotal"`
	Watermark   ListWatermark         `json:"watermark"`
}
