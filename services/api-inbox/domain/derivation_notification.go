package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"crypto/tls"
	gomail "github.com/wneessen/go-mail"
)

var derivationNotificationPhonePattern = regexp.MustCompile(`^[0-9]{8,15}$`)
var derivationNotificationTemplatePattern = regexp.MustCompile(`^[a-z0-9_]{1,512}$`)
var derivationNotificationLanguagePattern = regexp.MustCompile(`^[a-z]{2,3}(?:_[A-Z]{2})?$`)

// DerivationNotificationConfig controls the WhatsApp alert sent to the
// internal team when the agent transfers a conversation.
type DerivationNotificationConfig struct {
	Enabled           bool   `json:"enabled"`
	TemplateName      string `json:"templateName"`
	Language          string `json:"language"`
	SMTPParameterPath string `json:"smtpParameterPath"`
	RecipientPhone    string `json:"-"`
	EmailEnabled      bool   `json:"-"`
	EmailRecipient    string `json:"-"`
}

// DerivationSMTPConfig is loaded from the existing encrypted receipt SMTP parameter.
type DerivationSMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	From     string `json:"from"`
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

// MultiDerivationNotifier attempts every configured channel independently.
type MultiDerivationNotifier []DerivationNotifier

func (notifiers MultiDerivationNotifier) NotifyDerivation(ctx context.Context, notification DerivationNotification) error {
	var result error
	for _, notifier := range notifiers {
		if notifier != nil {
			result = errors.Join(result, notifier.NotifyDerivation(ctx, notification))
		}
	}
	return result
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
	contactURL, err := whatsappContactURL(notification.SourcePhone)
	if err != nil {
		return err
	}
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
	_, err = n.sender.SendTemplate(ctx, "agent-derivation", n.config.RecipientPhone, clientMessageID, SendTemplate{
		Name:       n.config.TemplateName,
		Language:   n.config.Language,
		Parameters: []string{contactURL, detail, label},
	})
	return err
}

// EmailDerivationNotifier sends a plain-text backup to the configured mailbox.
type EmailDerivationNotifier struct {
	config    DerivationSMTPConfig
	recipient string
}

func NewEmailDerivationNotifier(config DerivationSMTPConfig, recipient string) (*EmailDerivationNotifier, error) {
	recipient = strings.TrimSpace(recipient)
	fromAddress, fromErr := mail.ParseAddress(config.From)
	toAddress, toErr := mail.ParseAddress(recipient)
	if config.Host == "" || (config.Port != 465 && config.Port != 587) || config.User == "" || config.Password == "" || fromErr != nil || toErr != nil || fromAddress.Address == "" || toAddress.Address != recipient || strings.ContainsAny(config.Host+config.User+config.From+recipient, "\r\n") {
		return nil, errors.New("derivation email configuration is invalid")
	}
	return &EmailDerivationNotifier{config: config, recipient: recipient}, nil
}

func (n *EmailDerivationNotifier) NotifyDerivation(ctx context.Context, notification DerivationNotification) error {
	contactURL, err := whatsappContactURL(notification.SourcePhone)
	if err != nil {
		return err
	}
	detail := normalizeDerivationText(notification.Detail, 2000)
	if detail == "" {
		detail = "Consulta sin texto disponible"
	}
	label := normalizeDerivationText(notification.ReasonLabel, 200)
	if label == "" {
		label = "Requiere atencion del equipo"
	}
	message := gomail.NewMsg()
	if err := message.From(n.config.From); err != nil {
		return fmt.Errorf("configure derivation email sender: %w", err)
	}
	if err := message.To(n.recipient); err != nil {
		return fmt.Errorf("configure derivation email recipient: %w", err)
	}
	message.Subject("Conversacion de WhatsApp derivada")
	message.SetBodyString(gomail.TypeTextPlain, fmt.Sprintf("Telefono del cliente: %s\nWhatsApp: %s\nMotivo: %s\nDetalle: %s\nMensaje: %s\n", notification.SourcePhone, contactURL, label, detail, notification.MessageID))
	message.AddAlternativeString(gomail.TypeTextHTML, fmt.Sprintf("<p><strong>Telefono del cliente:</strong> <a href=\"%s\">%s</a></p><p><strong>Motivo:</strong> %s</p><p><strong>Detalle:</strong> %s</p><p><strong>Mensaje:</strong> %s</p>", html.EscapeString(contactURL), html.EscapeString(notification.SourcePhone), html.EscapeString(label), html.EscapeString(detail), html.EscapeString(notification.MessageID)))
	options := []gomail.Option{gomail.WithPort(n.config.Port), gomail.WithUsername(n.config.User), gomail.WithPassword(n.config.Password), gomail.WithSMTPAuth(gomail.SMTPAuthPlain), gomail.WithTLSConfig(&tls.Config{ServerName: n.config.Host, MinVersion: tls.VersionTLS12}), gomail.WithTimeout(20 * time.Second)}
	if n.config.Port == 465 {
		options = append(options, gomail.WithSSL())
	} else {
		options = append(options, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	client, err := gomail.NewClient(n.config.Host, options...)
	if err != nil {
		return fmt.Errorf("configure derivation SMTP: %w", err)
	}
	if err = client.DialAndSendWithContext(ctx, message); err != nil {
		return fmt.Errorf("send derivation SMTP: %w", err)
	}
	return nil
}

// NotifyAgentQuota emits a high-visibility operational alert without exposing the API key.
func (n *EmailDerivationNotifier) NotifyAgentQuota(ctx context.Context, model, detail string) error {
	model = normalizeDerivationText(model, 120)
	detail = normalizeDerivationText(detail, 1000)
	message := gomail.NewMsg()
	if err := message.From(n.config.From); err != nil {
		return fmt.Errorf("configure quota email sender: %w", err)
	}
	if err := message.To(n.recipient); err != nil {
		return fmt.Errorf("configure quota email recipient: %w", err)
	}
	message.Subject("URGENTE: agente IA sin cuota disponible")
	message.SetBodyString(gomail.TypeTextPlain, fmt.Sprintf("ALERTA CRITICA\n\nEl agente IA de WhatsApp no pudo responder porque Gemini agotó su cuota disponible.\nModelo: %s\nDetalle: %s\n\nRevisa la cuota o cambia la clave/modelo en SSM.", model, detail))
	message.AddAlternativeString(gomail.TypeTextHTML, fmt.Sprintf("<div style=\"font-family:Arial,sans-serif;border:4px solid #dc2626;padding:24px;background:#fff1f2;color:#450a0a\"><h1 style=\"margin-top:0;color:#b91c1c\">ALERTA CRÍTICA</h1><p><strong>El agente IA de WhatsApp no pudo responder porque Gemini agotó su cuota disponible.</strong></p><p><strong>Modelo:</strong> %s</p><p><strong>Detalle:</strong> %s</p><p>Revisa la cuota o cambia la clave/modelo en SSM.</p></div>", html.EscapeString(model), html.EscapeString(detail)))
	return n.send(ctx, message)
}

func (n *EmailDerivationNotifier) send(ctx context.Context, message *gomail.Msg) error {
	options := []gomail.Option{gomail.WithPort(n.config.Port), gomail.WithUsername(n.config.User), gomail.WithPassword(n.config.Password), gomail.WithSMTPAuth(gomail.SMTPAuthPlain), gomail.WithTLSConfig(&tls.Config{ServerName: n.config.Host, MinVersion: tls.VersionTLS12}), gomail.WithTimeout(20 * time.Second)}
	if n.config.Port == 465 {
		options = append(options, gomail.WithSSL())
	} else {
		options = append(options, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	client, err := gomail.NewClient(n.config.Host, options...)
	if err != nil {
		return fmt.Errorf("configure SMTP: %w", err)
	}
	if err = client.DialAndSendWithContext(ctx, message); err != nil {
		return fmt.Errorf("send SMTP: %w", err)
	}
	return nil
}

func whatsappContactURL(phone string) (string, error) {
	phone = strings.TrimPrefix(strings.TrimSpace(phone), "+")
	if !derivationNotificationPhonePattern.MatchString(phone) {
		return "", errors.New("derivation source phone is invalid")
	}
	return "https://wa.me/" + phone, nil
}
