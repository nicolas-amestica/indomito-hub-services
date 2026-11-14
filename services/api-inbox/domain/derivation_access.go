package domain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const derivationLinkLifetime = 24 * time.Hour
const derivationSessionLifetime = 30 * time.Minute

// ErrDerivationAccessInvalid no distingue entre permisos desconocidos, usados o vencidos.
var ErrDerivationAccessInvalid = errors.New("derivation access unavailable")
var derivationTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var derivationSessionIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var derivationBeforePattern = regexp.MustCompile(`^MSG#[0-9A-HJKMNP-TV-Z]{26}$`)

type derivationAccessDynamo interface {
	detailReader
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

type derivationAccessRecord struct {
	PK        string    `dynamodbav:"pk"`
	SK        string    `dynamodbav:"sk"`
	Phone     string    `dynamodbav:"phone"`
	Alias     string    `dynamodbav:"alias"`
	Reason    string    `dynamodbav:"reason"`
	DerivedAt time.Time `dynamodbav:"derivedAt"`
	ExpiresAt int64     `dynamodbav:"expiresAt"`
}

// DerivationAccessSession es un permiso de lectura separado de la sesión normal de Hub.
type DerivationAccessSession struct {
	SessionToken string    `json:"sessionToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// DerivationTranscriptMessage no expone claves S3, identificadores Meta ni datos operacionales.
type DerivationTranscriptMessage struct {
	ID        string      `json:"id"`
	Type      MessageType `json:"type"`
	Author    string      `json:"author"`
	CreatedAt time.Time   `json:"createdAt"`
	Text      string      `json:"text"`
}

// DerivationAccessPage contiene únicamente la conversación asociada al permiso.
type DerivationAccessPage struct {
	Phone      string                        `json:"phone"`
	Alias      string                        `json:"alias"`
	Reason     string                        `json:"reason"`
	DerivedAt  time.Time                     `json:"derivedAt"`
	ExpiresAt  time.Time                     `json:"expiresAt"`
	Messages   []DerivationTranscriptMessage `json:"messages"`
	NextBefore string                        `json:"nextBefore,omitempty"`
}

// DerivationAccessService emite permisos opacos de un uso y sesiones de solo lectura.
type DerivationAccessService struct {
	db       derivationAccessDynamo
	table    string
	hubURL   string
	now      func() time.Time
	newToken func() (string, error)
}

// NewDerivationAccessService reutiliza la tabla Inbox sin recursos ni índices adicionales.
func NewDerivationAccessService(db derivationAccessDynamo, table, hubURL string) (*DerivationAccessService, error) {
	parsed, err := url.Parse(hubURL)
	if db == nil || table == "" || err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || len(hubURL) > 200 || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("invalid derivation access configuration")
	}
	return &DerivationAccessService{db: db, table: table, hubURL: strings.TrimRight(hubURL, "/"), now: time.Now, newToken: randomDerivationToken}, nil
}

func randomDerivationToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate derivation permission: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
func derivationAccessKey(kind, token string) map[string]types.AttributeValue {
	digest := sha256.Sum256([]byte(token))
	return snapshotKey("DERIVATION_"+kind+"#"+hex.EncodeToString(digest[:]), "META")
}

// ConversationURL permite continuar con el inicio de sesión habitual después del vencimiento.
func (s *DerivationAccessService) ConversationURL(phone string) string {
	return s.hubURL + "/comunicaciones/conversaciones?telefono=" + url.QueryEscape(phone)
}

// CreateLink emite un permiso de lectura y guarda exclusivamente su hash.
func (s *DerivationAccessService) CreateLink(ctx context.Context, n DerivationNotification) (string, error) {
	if !derivationNotificationPhonePattern.MatchString(n.SourcePhone) {
		return "", ErrDerivationAccessInvalid
	}
	token, err := s.newToken()
	if err != nil {
		return "", err
	}
	key := derivationAccessKey("LINK", token)
	record := derivationAccessRecord{PK: key["pk"].(*types.AttributeValueMemberS).Value, SK: "META", Phone: n.SourcePhone, Alias: n.Alias, Reason: n.ReasonLabel, DerivedAt: n.DerivedAt, ExpiresAt: s.now().Add(derivationLinkLifetime).Unix()}
	item, err := attributevalue.MarshalMap(record)
	if err != nil {
		return "", err
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.table, Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")})
	if err != nil {
		return "", fmt.Errorf("store derivation permission: %w", err)
	}
	// El fragmento no viaja al servidor, Referer ni vista previa de WhatsApp.
	return s.hubURL + "/derivaciones/" + n.SourcePhone + "#token=" + token, nil
}

func (s *DerivationAccessService) readPermission(ctx context.Context, kind, token string) (derivationAccessRecord, error) {
	if !derivationTokenPattern.MatchString(token) {
		return derivationAccessRecord{}, ErrDerivationAccessInvalid
	}
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: derivationAccessKey(kind, token), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return derivationAccessRecord{}, fmt.Errorf("read derivation permission: %w", err)
	}
	var record derivationAccessRecord
	if err = attributevalue.UnmarshalMap(out.Item, &record); err != nil {
		return record, err
	}
	if record.ExpiresAt <= s.now().Unix() || !derivationNotificationPhonePattern.MatchString(record.Phone) {
		return derivationAccessRecord{}, ErrDerivationAccessInvalid
	}
	return record, nil
}

// Redeem consume el enlace atómicamente; GET y las vistas previas nunca lo consumen.
func (s *DerivationAccessService) Redeem(ctx context.Context, token string) (DerivationAccessSession, error) {
	record, err := s.readPermission(ctx, "LINK", token)
	if err != nil {
		return DerivationAccessSession{}, err
	}
	session, err := s.newToken()
	if err != nil {
		return DerivationAccessSession{}, err
	}
	expires := s.now().Add(derivationSessionLifetime).Truncate(time.Second)
	key := derivationAccessKey("SESSION", session)
	record.PK = key["pk"].(*types.AttributeValueMemberS).Value
	record.ExpiresAt = expires.Unix()
	item, err := attributevalue.MarshalMap(record)
	if err != nil {
		return DerivationAccessSession{}, err
	}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Delete: &types.Delete{TableName: &s.table, Key: derivationAccessKey("LINK", token), ConditionExpression: aws.String("attribute_exists(pk) AND expiresAt > :now"), ExpressionAttributeValues: map[string]types.AttributeValue{":now": &types.AttributeValueMemberN{Value: fmt.Sprint(s.now().Unix())}}}},
		{Put: &types.Put{TableName: &s.table, Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}})
	if err != nil {
		var cancelled *types.TransactionCanceledException
		if errors.As(err, &cancelled) {
			for _, reason := range cancelled.CancellationReasons {
				if aws.ToString(reason.Code) == "ConditionalCheckFailed" {
					return DerivationAccessSession{}, ErrDerivationAccessInvalid
				}
			}
		}
		return DerivationAccessSession{}, fmt.Errorf("redeem derivation permission: %w", err)
	}
	return DerivationAccessSession{SessionToken: session, ExpiresAt: expires}, nil
}

// Read recibe un identificador interno del contexto validado por el authorizer, nunca el token del navegador.
func (s *DerivationAccessService) Read(ctx context.Context, sessionID, before string) (DerivationAccessPage, error) {
	if !derivationSessionIDPattern.MatchString(sessionID) {
		return DerivationAccessPage{}, ErrDerivationAccessInvalid
	}
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: snapshotKey("DERIVATION_SESSION#"+sessionID, "META"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return DerivationAccessPage{}, fmt.Errorf("read authorized derivation: %w", err)
	}
	var record derivationAccessRecord
	if err = attributevalue.UnmarshalMap(out.Item, &record); err != nil {
		return DerivationAccessPage{}, err
	}
	// Estado de dominio: conserva revocación/TTL de la sesión ya autorizada en el Gateway.
	if record.ExpiresAt <= s.now().Unix() || !derivationNotificationPhonePattern.MatchString(record.Phone) {
		return DerivationAccessPage{}, ErrDerivationAccessInvalid
	}
	if before != "" && !derivationBeforePattern.MatchString(before) {
		return DerivationAccessPage{}, ErrDerivationAccessInvalid
	}
	messages, more, oldest, err := readMessages(ctx, s.db, s.table, record.Phone, before, 50)
	if err != nil {
		return DerivationAccessPage{}, err
	}
	page := DerivationAccessPage{Phone: record.Phone, Alias: record.Alias, Reason: record.Reason, DerivedAt: record.DerivedAt, ExpiresAt: time.Unix(record.ExpiresAt, 0).UTC(), Messages: make([]DerivationTranscriptMessage, 0, len(messages))}
	for _, message := range messages {
		page.Messages = append(page.Messages, derivationTranscript(message))
	}
	if more {
		page.NextBefore = oldest
	}
	return page, nil
}

func derivationTranscript(m Message) DerivationTranscriptMessage {
	author := "Equipo"
	switch m.Type {
	case MessageTypeCustomer:
		author = "Cliente"
	case MessageTypeAIAgent:
		author = "Agente IA"
	}
	var parts []string
	if m.Content != nil && strings.TrimSpace(*m.Content) != "" {
		parts = append(parts, *m.Content)
	}
	for _, a := range m.Attachments {
		name := a.FileName
		if name == "" {
			name = a.MIMEType
		}
		parts = append(parts, "Adjunto: "+name)
	}
	if m.Rich != nil {
		if reply := m.Rich.InteractiveReply; reply != nil {
			parts = append(parts, "Respuesta: "+reply.Title)
			if reply.Description != "" {
				parts = append(parts, reply.Description)
			}
		}
		if location := m.Rich.Location; location != nil {
			parts = append(parts, fmt.Sprintf("Ubicación: %s %s (%.6f, %.6f)", location.Name, location.Address, location.Latitude, location.Longitude))
		}
		for _, contact := range m.Rich.Contacts {
			parts = append(parts, "Contacto: "+contact.Name+" "+contact.Phone)
		}
		if interactive := m.Rich.Interactive; interactive != nil {
			parts = append(parts, interactive.Body)
			for _, option := range interactive.Options {
				parts = append(parts, option.Title+" "+option.Description)
			}
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "[Mensaje sin texto disponible]")
	}
	return DerivationTranscriptMessage{ID: m.ID, Type: m.Type, Author: author, CreatedAt: m.CreatedAt, Text: strings.Join(parts, "\n")}
}
