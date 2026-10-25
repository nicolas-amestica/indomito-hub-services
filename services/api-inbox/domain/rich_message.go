package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type MessageLocation struct {
	Latitude  float64 `json:"latitude" dynamodbav:"latitude"`
	Longitude float64 `json:"longitude" dynamodbav:"longitude"`
	Name      string  `json:"name" dynamodbav:"name,omitempty"`
	Address   string  `json:"address" dynamodbav:"address,omitempty"`
}

type MessageContact struct {
	Name    string `json:"name" dynamodbav:"name"`
	Phone   string `json:"phone" dynamodbav:"phone"`
	Email   string `json:"email" dynamodbav:"email,omitempty"`
	Company string `json:"company" dynamodbav:"company,omitempty"`
}

type MessageInteractiveReply struct {
	Kind        string `json:"kind" dynamodbav:"kind"`
	ID          string `json:"id" dynamodbav:"id"`
	Title       string `json:"title" dynamodbav:"title"`
	Description string `json:"description" dynamodbav:"description,omitempty"`
}

type MessageInteractiveOption struct {
	ID          string `json:"id" dynamodbav:"id"`
	Title       string `json:"title" dynamodbav:"title"`
	Description string `json:"description" dynamodbav:"description,omitempty"`
}

type MessageInteractive struct {
	Kind    string                     `json:"kind" dynamodbav:"kind"`
	Body    string                     `json:"body" dynamodbav:"body"`
	Footer  string                     `json:"footer" dynamodbav:"footer,omitempty"`
	Button  string                     `json:"button" dynamodbav:"button,omitempty"`
	Options []MessageInteractiveOption `json:"options" dynamodbav:"options"`
}

type MessageRichContent struct {
	Kind             string                   `json:"kind" dynamodbav:"kind"`
	Location         *MessageLocation         `json:"location,omitempty" dynamodbav:"location,omitempty"`
	Contacts         []MessageContact         `json:"contacts,omitempty" dynamodbav:"contacts,omitempty"`
	InteractiveReply *MessageInteractiveReply `json:"interactiveReply,omitempty" dynamodbav:"interactiveReply,omitempty"`
	Interactive      *MessageInteractive      `json:"interactive,omitempty" dynamodbav:"interactive,omitempty"`
}

type richMessageProvider interface {
	SendRichMessage(context.Context, string, MessageRichContent) (string, error)
}

func (s *SendService) SendRichMessage(ctx context.Context, ownerID, phone, clientMessageID string, rich MessageRichContent) (SendState, error) {
	provider, ok := s.provider.(richMessageProvider)
	if !ok {
		return SendState{}, errors.New("rich message provider is unavailable")
	}
	now := s.now().UTC()
	last, err := s.repository.LastCustomerMessageAt(ctx, phone)
	if err != nil {
		return SendState{}, err
	}
	if last == nil || now.Sub(last.UTC()) < 0 || now.Sub(last.UTC()) >= 24*time.Hour {
		return SendState{}, ErrServiceWindow
	}
	encoded, err := json.Marshal(rich)
	if err != nil {
		return SendState{}, err
	}
	preview := richPreview(rich)
	digest := sha256.Sum256(append([]byte(phone+"\x00rich\x00"), encoded...))
	state, created, err := s.repository.Reserve(ctx, SendReservation{OwnerID: ownerID, Phone: phone, ClientMessageID: clientMessageID, RequestHash: hex.EncodeToString(digest[:]), Content: preview, Now: now, Actor: MessageTypeTeam, Rich: &rich})
	if err != nil || !created {
		return state, err
	}
	state, err = s.repository.MarkProviderPending(ctx, ownerID, clientMessageID, now)
	if err != nil {
		return state, err
	}
	wamid, providerErr := provider.SendRichMessage(ctx, phone, rich)
	if providerErr != nil {
		return s.failProviderSend(ctx, ownerID, clientMessageID, state, providerErr)
	}
	if wamid == "" {
		return s.repository.Fail(ctx, ownerID, clientMessageID, SendUncertain, "El proveedor no confirmo el identificador", s.now().UTC())
	}
	return s.repository.Commit(ctx, ownerID, clientMessageID, wamid, s.now().UTC())
}

func richPreview(rich MessageRichContent) string {
	switch rich.Kind {
	case "location":
		if rich.Location != nil && strings.TrimSpace(rich.Location.Name) != "" {
			return "[Ubicacion] " + strings.TrimSpace(rich.Location.Name)
		}
		return "[Ubicacion]"
	case "contacts":
		if len(rich.Contacts) > 0 {
			return "[Contacto] " + strings.TrimSpace(rich.Contacts[0].Name)
		}
		return "[Contacto]"
	case "sticker":
		return "[Sticker]"
	case "interactive_reply":
		if rich.InteractiveReply != nil {
			return strings.TrimSpace(rich.InteractiveReply.Title)
		}
	case "interactive":
		if rich.Interactive != nil {
			return strings.TrimSpace(rich.Interactive.Body)
		}
	}
	return "[Mensaje]"
}
