package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SendStateName string

const (
	SendReserved        SendStateName = "reserved"
	SendProviderPending SendStateName = "provider_pending"
	SendRecovering      SendStateName = "recovering"
	SendUncertain       SendStateName = "uncertain"
	SendFailedRetryable SendStateName = "failed_retryable"
	SendCommitted       SendStateName = "committed"
	SendFailed          SendStateName = "failed"
)

var (
	ErrSendConflict       = errors.New("send idempotency conflict")
	ErrServiceWindow      = errors.New("whatsapp service window closed")
	ErrProviderRejected   = errors.New("whatsapp provider rejected send")
	ErrSendNotFound       = errors.New("send not found")
	ErrSendNotRetryable   = errors.New("send is not retryable")
	ErrSendRetryExhausted = errors.New("send retry is exhausted")
	ErrSendNotResolvable  = errors.New("send is not resolvable")
)

type SendAttachment struct {
	FileName string `json:"fileName" dynamodbav:"fileName"`
	MIMEType string `json:"mimeType" dynamodbav:"mimeType"`
	Size     int64  `json:"size" dynamodbav:"size"`
	Key      string `json:"-" dynamodbav:"key,omitempty"`
}

type SendResult struct {
	WhatsAppMessageID string    `json:"whatsappMessageId" dynamodbav:"whatsappMessageId"`
	MessageSK         string    `json:"msgSk" dynamodbav:"msgSk"`
	CreatedAt         time.Time `json:"createdAt" dynamodbav:"createdAt"`
}

type SendState struct {
	ClientMessageID    string          `json:"clientMessageId" dynamodbav:"clientMessageId"`
	Phone              string          `json:"phone" dynamodbav:"phone"`
	State              SendStateName   `json:"state" dynamodbav:"state"`
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
	Result             *SendResult     `json:"result,omitempty" dynamodbav:"result,omitempty"`
}

type SendReservation struct {
	OwnerID         string
	Phone           string
	ClientMessageID string
	RequestHash     string
	Content         string
	Attachment      *SendAttachment
	Now             time.Time
	Actor           MessageType
}

type SendRepository interface {
	LastCustomerMessageAt(context.Context, string) (*time.Time, error)
	Reserve(context.Context, SendReservation) (SendState, bool, error)
	MarkProviderPending(context.Context, string, string, time.Time) (SendState, error)
	Commit(context.Context, string, string, string, time.Time) (SendState, error)
	Fail(context.Context, string, string, SendStateName, string, time.Time) (SendState, error)
	Owned(context.Context, string, string) (SendState, error)
	ClaimRetry(context.Context, string, string, time.Time) (SendState, string, error)
}

type TextProvider interface {
	SendText(context.Context, string, string) (string, error)
}

type mediaProvider interface {
	SendMedia(context.Context, string, string, string, string, []byte) (string, error)
}

type ProviderError struct {
	Retryable, TransmissionPossible bool
	Cause                           error
}

func (e *ProviderError) Error() string { return e.Cause.Error() }
func (e *ProviderError) Unwrap() error { return e.Cause }

type SendService struct {
	repository SendRepository
	provider   TextProvider
	now        func() time.Time
}

func NewSendService(repository SendRepository, provider TextProvider, now func() time.Time) (*SendService, error) {
	if repository == nil || provider == nil {
		return nil, errors.New("send dependencies are required")
	}
	if now == nil {
		now = time.Now
	}
	return &SendService{repository: repository, provider: provider, now: now}, nil
}

func (s *SendService) SendText(ctx context.Context, ownerID, phone, clientMessageID, content string) (SendState, error) {
	return s.sendText(ctx, ownerID, phone, clientMessageID, content, MessageTypeTeam)
}

func (s *SendService) SendAgentText(ctx context.Context, phone, clientMessageID, content string) (SendState, error) {
	return s.sendText(ctx, "agent", phone, clientMessageID, content, MessageTypeAIAgent)
}

// SendAgentMedia sends and journals a permanent document selected by the agent.
func (s *SendService) SendAgentMedia(ctx context.Context, phone, clientMessageID, fileName, mimeType, caption string, content []byte) (SendState, error) {
	provider, ok := s.provider.(mediaProvider)
	if !ok {
		return SendState{}, errors.New("media provider is unavailable")
	}
	now := s.now().UTC()
	last, err := s.repository.LastCustomerMessageAt(ctx, phone)
	if err != nil {
		return SendState{}, err
	}
	if last == nil || now.Sub(last.UTC()) < 0 || now.Sub(last.UTC()) >= 24*time.Hour {
		return SendState{}, ErrServiceWindow
	}
	digest := sha256.Sum256(append([]byte(phone+"\x00media\x00"+fileName+"\x00"+caption+"\x00"), content...))
	attachment := &SendAttachment{FileName: fileName, MIMEType: mimeType, Size: int64(len(content))}
	state, created, err := s.repository.Reserve(ctx, SendReservation{OwnerID: "agent", Phone: phone, ClientMessageID: clientMessageID, RequestHash: hex.EncodeToString(digest[:]), Content: caption, Attachment: attachment, Now: now, Actor: MessageTypeAIAgent})
	if err != nil || !created {
		return state, err
	}
	state, err = s.repository.MarkProviderPending(ctx, "agent", clientMessageID, now)
	if err != nil {
		return state, err
	}
	wamid, providerErr := provider.SendMedia(ctx, phone, fileName, mimeType, caption, content)
	if providerErr != nil {
		failed, persistErr := s.repository.Fail(ctx, "agent", clientMessageID, SendFailed, "El proveedor rechazó el documento", s.now().UTC())
		if persistErr != nil {
			return state, persistErr
		}
		return failed, fmt.Errorf("send agent media: %w", providerErr)
	}
	if wamid == "" {
		return s.repository.Fail(ctx, "agent", clientMessageID, SendUncertain, "El proveedor no confirmó el documento", s.now().UTC())
	}
	return s.repository.Commit(ctx, "agent", clientMessageID, wamid, s.now().UTC())
}

func (s *SendService) sendText(ctx context.Context, ownerID, phone, clientMessageID, content string, actor MessageType) (SendState, error) {
	content = strings.TrimSpace(content)
	now := s.now().UTC()
	last, err := s.repository.LastCustomerMessageAt(ctx, phone)
	if err != nil {
		return SendState{}, err
	}
	if last == nil || now.Sub(last.UTC()) < 0 || now.Sub(last.UTC()) >= 24*time.Hour {
		return SendState{}, ErrServiceWindow
	}
	hash := sha256.Sum256([]byte(phone + "\x00text\x00" + content))
	state, created, err := s.repository.Reserve(ctx, SendReservation{OwnerID: ownerID, Phone: phone, ClientMessageID: clientMessageID, RequestHash: hex.EncodeToString(hash[:]), Content: content, Now: now, Actor: actor})
	if err != nil || !created {
		return state, err
	}
	state, err = s.repository.MarkProviderPending(ctx, ownerID, clientMessageID, now)
	if err != nil {
		return state, err
	}
	wamid, providerErr := s.provider.SendText(ctx, phone, content)
	if providerErr != nil {
		name, message := SendFailed, "El proveedor rechazo el envio"
		var classified *ProviderError
		if errors.As(providerErr, &classified) {
			if classified.TransmissionPossible {
				name, message = SendUncertain, "No fue posible confirmar la entrega"
			} else if classified.Retryable {
				name, message = SendFailedRetryable, "El envio puede reintentarse"
			}
		}
		failed, persistErr := s.repository.Fail(ctx, ownerID, clientMessageID, name, message, s.now().UTC())
		if persistErr != nil {
			return state, persistErr
		}
		if name == SendUncertain {
			return failed, nil
		}
		return failed, ErrProviderRejected
	}
	if wamid == "" {
		uncertain, persistErr := s.repository.Fail(ctx, ownerID, clientMessageID, SendUncertain, "El proveedor no confirmo el identificador", s.now().UTC())
		return uncertain, persistErr
	}
	return s.repository.Commit(ctx, ownerID, clientMessageID, wamid, s.now().UTC())
}

func (s *SendService) Get(ctx context.Context, ownerID, id string) (SendState, error) {
	return s.repository.Owned(ctx, ownerID, id)
}

func (s *SendService) Retry(ctx context.Context, ownerID, id string) (SendState, error) {
	state, content, err := s.repository.ClaimRetry(ctx, ownerID, id, s.now().UTC())
	if err != nil {
		return SendState{}, err
	}
	wamid, providerErr := s.provider.SendText(ctx, state.Phone, content)
	if providerErr != nil {
		name, message := SendFailed, "El proveedor rechazo el envio"
		var classified *ProviderError
		if errors.As(providerErr, &classified) {
			if classified.TransmissionPossible {
				name, message = SendUncertain, "No fue posible confirmar la entrega"
			} else if classified.Retryable {
				name, message = SendFailed, "El reintento no pudo completarse"
			}
		}
		failed, persistErr := s.repository.Fail(ctx, ownerID, id, name, message, s.now().UTC())
		if persistErr != nil {
			return state, persistErr
		}
		if name == SendUncertain {
			return failed, nil
		}
		return failed, ErrProviderRejected
	}
	if wamid == "" {
		return s.repository.Fail(ctx, ownerID, id, SendUncertain, "El proveedor no confirmo el identificador", s.now().UTC())
	}
	return s.repository.Commit(ctx, ownerID, id, wamid, s.now().UTC())
}

func (s *SendService) Resolve(ctx context.Context, ownerID, id, resolution, wamid, reason string) (SendState, error) {
	state, err := s.repository.Owned(ctx, ownerID, id)
	if err != nil {
		return SendState{}, err
	}
	if state.State != SendUncertain {
		return SendState{}, ErrSendNotResolvable
	}
	if resolution == "delivered" {
		return s.repository.Commit(ctx, ownerID, id, wamid, s.now().UTC())
	}
	return s.repository.Fail(ctx, ownerID, id, SendFailed, sanitizeSendText(reason), s.now().UTC())
}
