package domain

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"

	"ind-hub-api-gox-sls-pri-gh/libs/brandedemail"

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
	HubBaseURL        string `json:"hubBaseUrl"`
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
	SourcePhone        string
	MessageID          string
	Detail             string
	ReasonLabel        string
	Alias              string
	DerivedAt          time.Time
	History            []DerivationTranscriptMessage
	HistoryUnavailable bool
	ConversationURL    string
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
	access *DerivationAccessService
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

// SetAccessLinks habilita el enlace temporal dentro del detalle de la plantilla existente.
func (n *WhatsAppDerivationNotifier) SetAccessLinks(access *DerivationAccessService) {
	n.access = access
}

// NotifyDerivation conserva las tres variables de la plantilla aprobada.
func (n *WhatsAppDerivationNotifier) NotifyDerivation(ctx context.Context, notification DerivationNotification) error {
	contactURL, err := whatsappContactURL(notification.SourcePhone)
	if err != nil {
		return err
	}
	detail := normalizeDerivationText(strings.Join(strings.Fields(notification.Detail), " "), 900)
	if detail == "" {
		detail = "Consulta sin texto disponible"
	}
	label := normalizeDerivationText(strings.Join(strings.Fields(notification.ReasonLabel), " "), 120)
	if label == "" {
		label = "Requiere atencion del equipo"
	}
	if n.access != nil {
		link, linkErr := n.access.CreateLink(ctx, notification)
		linkLabel := " | Ver conversación en Hub (enlace de un uso; abrir en 24 h): "
		if linkErr != nil {
			// La derivación sigue llegando aunque falle la emisión del permiso.
			emitProviderFailure("DerivationAccessFailures", "create")
			link = n.access.ConversationURL(notification.SourcePhone)
			linkLabel = " | Abrir conversación en Hub (requiere iniciar sesión): "
		}
		suffix := linkLabel + link
		detail = normalizeDerivationText(detail, 900-len([]rune(suffix))) + suffix
	}
	digest := sha256.Sum256([]byte(notification.SourcePhone + "\x00" + notification.MessageID + "\x00" + label))
	clientMessageID := "derivation-" + hex.EncodeToString(digest[:16])
	_, err = n.sender.SendTemplate(ctx, "agent-derivation", n.config.RecipientPhone, clientMessageID, SendTemplate{
		Name:       n.config.TemplateName,
		Language:   n.config.Language,
		Parameters: []string{contactURL, detail, label},
	})
	return err
}

// EmailDerivationNotifier envía el contexto con diseño de marca y alternativa de texto plano.
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
	message, err := n.derivationMessage(notification)
	if err != nil {
		return err
	}
	return n.send(ctx, message)
}

func (n *EmailDerivationNotifier) derivationMessage(notification DerivationNotification) (*gomail.Msg, error) {
	contactURL, err := whatsappContactURL(notification.SourcePhone)
	if err != nil {
		return nil, err
	}
	label := normalizeDerivationText(notification.ReasonLabel, 200)
	if label == "" {
		label = "Requiere atención del equipo"
	}
	detail := notification.Detail
	if strings.TrimSpace(detail) == "" {
		detail = "Consulta sin texto disponible"
	}
	content := brandedemail.Content{Preheader: "Una conversación de WhatsApp necesita atención del equipo", Eyebrow: "Derivación de WhatsApp", Title: "Una conversación necesita tu atención", Lead: "Revisa el contexto antes de contactar al cliente.", Body: detail, Details: []brandedemail.Detail{{Label: "Teléfono del cliente", Value: notification.SourcePhone}, {Label: "Motivo", Value: label}}, Actions: []brandedemail.Action{{Label: "Contactar por WhatsApp", URL: contactURL}}}
	if notification.Alias != "" && notification.Alias != notification.SourcePhone {
		content.Details = append(content.Details, brandedemail.Detail{Label: "Cliente", Value: notification.Alias})
	}
	if !notification.DerivedAt.IsZero() {
		content.Details = append(content.Details, brandedemail.Detail{Label: "Fecha de derivación", Value: derivationEmailTime(notification.DerivedAt)})
	}
	if notification.ConversationURL != "" {
		content.Actions = append(content.Actions, brandedemail.Action{Label: "Abrir conversación en Hub", URL: notification.ConversationURL})
	}
	var plain strings.Builder
	fmt.Fprintf(&plain, "Teléfono del cliente: %s\nMotivo: %s\nDetalle: %s\nWhatsApp: %s\n", notification.SourcePhone, label, detail, contactURL)
	if notification.ConversationURL != "" {
		fmt.Fprintf(&plain, "Conversación en Hub: %s\n", notification.ConversationURL)
	}
	plain.WriteString("\nÚltimos mensajes de la conversación (del más antiguo al más reciente):\n")
	for _, m := range notification.History {
		timestamp := derivationEmailTime(m.CreatedAt)
		content.Messages = append(content.Messages, brandedemail.Message{Author: m.Author, Timestamp: timestamp, Text: m.Text})
		fmt.Fprintf(&plain, "\n[%s] %s:\n%s\n", timestamp, m.Author, m.Text)
	}
	if notification.HistoryUnavailable {
		content.HistoryNotice = "No se pudo recuperar todo el historial. Abre la conversación en Hub para revisar el contexto completo."
	} else {
		content.HistoryNotice = fmt.Sprintf("Se incluyen los últimos %d mensajes disponibles entre el cliente y el agente IA, en orden cronológico.", len(notification.History))
	}
	content.Notice = "Para responder o gestionar la conversación, ingresa a Indómito Hub con tu cuenta."
	fmt.Fprintf(&plain, "\n%s\nReferencia técnica: %s\n", content.HistoryNotice, notification.MessageID)
	content.Reference = notification.MessageID
	rendered, err := brandedemail.Render(content)
	if err != nil {
		return nil, err
	}
	message := gomail.NewMsg()
	if err = message.From(n.config.From); err != nil {
		return nil, fmt.Errorf("configure derivation email sender: %w", err)
	}
	if err = message.To(n.recipient); err != nil {
		return nil, fmt.Errorf("configure derivation email recipient: %w", err)
	}
	message.Subject("Conversación de WhatsApp derivada")
	message.SetBodyString(gomail.TypeTextPlain, plain.String())
	message.AddAlternativeString(gomail.TypeTextHTML, rendered)
	return message, nil
}

func derivationEmailTime(value time.Time) string {
	if value.IsZero() {
		return "Fecha no disponible"
	}
	location, err := time.LoadLocation("America/Santiago")
	if err != nil {
		return value.UTC().Format("02/01/2006 15:04 UTC")
	}
	return value.In(location).Format("02/01/2006 15:04 -07:00")
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
