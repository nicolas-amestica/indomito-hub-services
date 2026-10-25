package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

var derivationNotificationPhonePattern = regexp.MustCompile(`^[0-9]{8,15}$`)
var derivationNotificationTemplatePattern = regexp.MustCompile(`^[a-z0-9_]{1,512}$`)
var derivationNotificationLanguagePattern = regexp.MustCompile(`^[a-z]{2,3}(?:_[A-Z]{2})?$`)

// DerivationNotificationConfig controls the WhatsApp alert sent to the
// internal team when the agent transfers a conversation.
type DerivationNotificationConfig struct {
	Enabled        bool   `json:"enabled"`
	RecipientPhone string `json:"recipientPhone"`
	TemplateName   string `json:"templateName"`
	Language       string `json:"language"`
}

// DerivationNotification contains the safe context rendered in the approved
// WhatsApp template.
type DerivationNotification struct {
	SourcePhone string
	MessageID   string
	Detail      string
	ReasonLabel string
}

// DerivationNotifier alerts the internal team without exposing provider
// details to the agent orchestration.
type DerivationNotifier interface {
	NotifyDerivation(context.Context, DerivationNotification) error
}

type derivationTemplateSender interface {
	SendTemplate(context.Context, string, string, string, SendTemplate) (SendState, error)
}

// WhatsAppDerivationNotifier sends an idempotently journaled approved template
// to the configured internal number.
type WhatsAppDerivationNotifier struct {
	sender derivationTemplateSender
	config DerivationNotificationConfig
}

// NewWhatsAppDerivationNotifier validates and constructs the internal alert.
func NewWhatsAppDerivationNotifier(sender derivationTemplateSender, config DerivationNotificationConfig) (*WhatsAppDerivationNotifier, error) {
	config.RecipientPhone = strings.TrimPrefix(strings.TrimSpace(config.RecipientPhone), "+")
	config.TemplateName = strings.TrimSpace(config.TemplateName)
	config.Language = strings.TrimSpace(config.Language)
	if sender == nil || !config.Enabled || !derivationNotificationPhonePattern.MatchString(config.RecipientPhone) || !derivationNotificationTemplatePattern.MatchString(config.TemplateName) || !derivationNotificationLanguagePattern.MatchString(config.Language) {
		return nil, errors.New("derivation notification configuration is invalid")
	}
	return &WhatsAppDerivationNotifier{sender: sender, config: config}, nil
}

// NotifyDerivation sends customer phone, latest question and derivation label.
func (n *WhatsAppDerivationNotifier) NotifyDerivation(ctx context.Context, notification DerivationNotification) error {
	detail := normalizeDerivationText(notification.Detail, 900)
	if detail == "" {
		detail = "Consulta sin texto disponible"
	}
	label := normalizeDerivationText(notification.ReasonLabel, 120)
	if label == "" {
		label = "Requiere atencion del equipo"
	}
	digest := sha256.Sum256([]byte(notification.SourcePhone + "\x00" + notification.MessageID + "\x00" + detail + "\x00" + label))
	clientMessageID := "derivation-" + hex.EncodeToString(digest[:16])
	_, err := n.sender.SendTemplate(ctx, "agent-derivation", n.config.RecipientPhone, clientMessageID, SendTemplate{
		Name:       n.config.TemplateName,
		Language:   n.config.Language,
		Parameters: []string{notification.SourcePhone, detail, label},
	})
	return err
}
