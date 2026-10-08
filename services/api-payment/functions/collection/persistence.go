// Package collection persiste comandos financieros y su auditoría en DynamoDB sin Scan.
package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// Database permite probar las garantías transaccionales sin depender de AWS.
type Database interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

// queryDatabase es opcional: los comandos financieros básicos solo usan claves directas.
type queryDatabase interface {
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// Service coordina escrituras condicionales; no ejecuta efectos bancarios dentro de la transacción.
type Service struct {
	DB    Database
	Table string
}

// Command se construye en un handler autorizado, nunca directamente desde el JSON público.
// Reference es una identidad externa global canónica, no el ID de intento elegido por el cliente.
type Command struct {
	ID                 string
	AccountID          string
	ExpectedVersion    int64
	Payload            any
	Reference          string
	Attempt            *collection.Attempt
	ReceiptID          string
	ConfirmedAttemptID string `json:",omitempty"`
	ResolvedAttemptID  string `json:",omitempty"`
	ReceiptEmail       string `json:",omitempty"`
	PaymentSessionID   string `json:",omitempty"`
}

// ErrReplayMismatch impide reutilizar una clave de idempotencia con otra entrada.
var ErrReplayMismatch = errors.New("clave de operacion reutilizada con otra entrada")

// ErrReferenceUsed impide contabilizar una referencia bancaria/proveedor dos veces.
var ErrReferenceUsed = errors.New("referencia financiera ya registrada")

// ErrNotFound indica una cuenta todavía no publicada o inexistente.
var ErrNotFound = errors.New("cuenta no encontrada")

type record struct {
	AttemptID            string                         `dynamodbav:"attemptId,omitempty"`
	ProviderStatus       string                         `dynamodbav:"providerStatus,omitempty"`
	ProviderDetail       string                         `dynamodbav:"providerDetail,omitempty"`
	CheckedAt            int64                          `dynamodbav:"checkedAt,omitempty"`
	ProviderNotification *ProviderNotification          `dynamodbav:"providerNotification,omitempty"`
	RelatedAccountID     string                         `dynamodbav:"relatedAccountId,omitempty"`
	PendingAnnexID       string                         `dynamodbav:"pendingAnnexId,omitempty"`
	PendingGroupID       string                         `dynamodbav:"pendingGroupId,omitempty"`
	BasisPoints          int64                          `dynamodbav:"basisPoints,omitempty"`
	LookupKey            string                         `dynamodbav:"lookupKey,omitempty"`
	DeliveryID           string                         `dynamodbav:"deliveryId,omitempty"`
	DeliveryAttempts     int64                          `dynamodbav:"deliveryAttempts,omitempty"`
	DeliveryMode         string                         `dynamodbav:"deliveryMode,omitempty"`
	LastFailureCode      string                         `dynamodbav:"lastFailureCode,omitempty"`
	RetryAudit           *collection.Audit              `dynamodbav:"retryAudit,omitempty"`
	RequestedBy          string                         `dynamodbav:"requestedBy,omitempty"`
	PaymentSessionID     string                         `dynamodbav:"paymentSessionId,omitempty"`
	NextAttemptAt        int64                          `dynamodbav:"nextAttemptAt,omitempty"`
	DocumentKey          string                         `dynamodbav:"documentKey,omitempty"`
	DocumentSHA256       string                         `dynamodbav:"documentSha256,omitempty"`
	DocumentVersion      int64                          `dynamodbav:"documentVersion,omitempty"`
	PassengerName        string                         `dynamodbav:"passengerName,omitempty"`
	PassengerDocument    string                         `dynamodbav:"passengerDocument,omitempty"`
	ReceiptEmail         string                         `dynamodbav:"receiptEmail,omitempty"`
	Checkout             *CheckoutResult                `dynamodbav:"checkout,omitempty"`
	TripID               string                         `dynamodbav:"tripId,omitempty"`
	AccountID            string                         `dynamodbav:"accountId,omitempty"`
	Count                int64                          `dynamodbav:"count,omitempty"`
	ExpiresAt            int64                          `dynamodbav:"expiresAt,omitempty"`
	Startup              *collection.Startup            `dynamodbav:"startup,omitempty"`
	PK                   string                         `dynamodbav:"pk"`
	SK                   string                         `dynamodbav:"sk"`
	Version              int64                          `dynamodbav:"version,omitempty"`
	Fingerprint          string                         `dynamodbav:"fingerprint,omitempty"`
	Account              *collection.Account            `dynamodbav:"account,omitempty"`
	Change               *collection.Change             `dynamodbav:"change,omitempty"`
	Event                *collection.Event              `dynamodbav:"event,omitempty"`
	Approval             *collection.Audit              `dynamodbav:"approval,omitempty"`
	Roster               *RosterProjection              `dynamodbav:"roster,omitempty"`
	Attempt              *collection.Attempt            `dynamodbav:"attempt,omitempty"`
	ReceiptID            string                         `dynamodbav:"receiptId,omitempty"`
	Status               string                         `dynamodbav:"status,omitempty"`
	Prepared             int64                          `dynamodbav:"prepared,omitempty"`
	Applied              int64                          `dynamodbav:"applied,omitempty"`
	Expected             int64                          `dynamodbav:"expected,omitempty"`
	RosterSchemaVersion  int                            `dynamodbav:"rosterSchemaVersion,omitempty"`
	RosterClosed         bool                           `dynamodbav:"rosterClosed,omitempty"`
	RosterClosure        *collection.Audit              `dynamodbav:"rosterClosure,omitempty"`
	Settlement           *collection.Settlement         `dynamodbav:"settlement,omitempty"`
	Supplier             *collection.SupplierCommitment `dynamodbav:"supplier,omitempty"`
	Terms                *PaymentTerms                  `dynamodbav:"terms,omitempty"`
	CodeKey              string                         `dynamodbav:"codeKey,omitempty"`
	TripCode             string                         `dynamodbav:"tripCode,omitempty"`
	CodeVersion          int64                          `dynamodbav:"codeVersion,omitempty"`
	AccessAudit          *collection.Audit              `dynamodbav:"accessAudit,omitempty"`
	TaxRequest           *TaxDocumentRequest            `dynamodbav:"taxRequest,omitempty"`
}

type PaymentTerms struct {
	DepartureDate string `dynamodbav:"departureDate"`
}

// RosterProjection permite listar la nómina vigente por partición de gira, sin GSI.
// Es exclusivamente administrativa; el portal público no consulta estas filas.
type RosterProjection struct {
	AccountID             string `json:"accountId" dynamodbav:"accountId"`
	ParticipantID         string `json:"participantId" dynamodbav:"participantId"`
	PreviousParticipation string `json:"previousParticipation,omitempty" dynamodbav:"previousParticipation,omitempty"`
	NextParticipation     string `json:"nextParticipation,omitempty" dynamodbav:"nextParticipation,omitempty"`
	Name                  string `json:"name" dynamodbav:"name"`
	Document              string `json:"document" dynamodbav:"document"`
	Active                bool   `json:"active" dynamodbav:"active"`
	Free                  bool   `json:"free" dynamodbav:"free"`
	Version               int64  `json:"version" dynamodbav:"version"`
}

func (s Service) activePlanCheck(tripID string) types.TransactWriteItem {
	return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
		TableName: &s.Table, Key: key("TRIP#"+tripID, "META"),
		ConditionExpression:       aws.String("#status = :active"),
		ExpressionAttributeNames:  map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":active": &types.AttributeValueMemberS{Value: "ACTIVE"}},
	}}
}

func (s Service) openRosterCheck(tripID string) types.TransactWriteItem {
	return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
		TableName: &s.Table, Key: key("TRIP#"+tripID, "META"),
		ConditionExpression:      aws.String("#status = :active AND (attribute_not_exists(rosterClosed) OR rosterClosed = :false)"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":active": &types.AttributeValueMemberS{Value: "ACTIVE"},
			":false":  &types.AttributeValueMemberBOOL{Value: false},
		},
	}}
}

func (s Service) updatingAnnexCheck(tripID, annexID string) types.TransactWriteItem {
	return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
		TableName: &s.Table, Key: key("TRIP#"+tripID, "META"),
		ConditionExpression:      aws.String("#status = :updating AND pendingAnnexId = :annex"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":updating": &types.AttributeValueMemberS{Value: "UPDATING_ROSTER"},
			":annex":    &types.AttributeValueMemberS{Value: annexID},
		},
	}}
}

func (s Service) applyingGroupCheck(tripID, commandID string) types.TransactWriteItem {
	return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
		TableName: &s.Table, Key: key("TRIP#"+tripID, "META"),
		ConditionExpression:      aws.String("#status = :applying AND pendingGroupId = :command"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":applying": &types.AttributeValueMemberS{Value: "APPLYING_GROUP_DEPOSIT"},
			":command":  &types.AttributeValueMemberS{Value: commandID},
		},
	}}
}

func (s Service) applyingGroupDiscountCheck(tripID, discountID string) types.TransactWriteItem {
	return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
		TableName: &s.Table, Key: key("TRIP#"+tripID, "META"),
		ConditionExpression:      aws.String("#status = :applying AND pendingGroupId = :command"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":applying": &types.AttributeValueMemberS{Value: "APPLYING_GROUP_DISCOUNT"},
			":command":  &types.AttributeValueMemberS{Value: discountID},
		},
	}}
}

func (s Service) preparingGroupDiscountCheck(tripID, discountID string) types.TransactWriteItem {
	return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
		TableName: &s.Table, Key: key("TRIP#"+tripID, "META"),
		ConditionExpression:      aws.String("#status = :preparing AND pendingGroupId = :command"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":preparing": &types.AttributeValueMemberS{Value: "PREPARING_GROUP_DISCOUNT"},
			":command":   &types.AttributeValueMemberS{Value: discountID},
		},
	}}
}

func (s Service) accountVersionCheck(accountID string, version int64) types.TransactWriteItem {
	return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
		TableName: &s.Table, Key: key("ACCOUNT#"+accountID, "META"),
		ConditionExpression:      aws.String("#version = :version"),
		ExpressionAttributeNames: map[string]string{"#version": "version"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":version": &types.AttributeValueMemberN{Value: strconv.FormatInt(version, 10)},
		},
	}}
}

func key(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}
}
func (s Service) read(ctx context.Context, pk, sk string) (record, error) {
	var r record
	out, err := s.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.Table, Key: key(pk, sk), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return r, fmt.Errorf("leer operacion: %w", err)
	}
	if len(out.Item) == 0 {
		return r, ErrNotFound
	}
	if err = attributevalue.UnmarshalMap(out.Item, &r); err != nil {
		return r, fmt.Errorf("decodificar operacion: %w", err)
	}
	return r, nil
}

// GetAccount usa una lectura consistente por clave, sin consultas globales ni índices.
func (s Service) GetAccount(ctx context.Context, id string) (collection.Account, error) {
	r, err := s.read(ctx, "ACCOUNT#"+id, "META")
	if err != nil {
		return collection.Account{}, err
	}
	if r.Account == nil || r.Version != r.Account.Version {
		return collection.Account{}, collection.ErrInvalid
	}
	plan, err := s.read(ctx, "TRIP#"+r.Account.TripID, "META")
	if err != nil {
		return collection.Account{}, err
	}
	if plan.Status != "ACTIVE" {
		return collection.Account{}, ErrNotFound
	}
	return *r.Account, r.Account.Validate()
}

func (s Service) put(r record, condition string, values map[string]types.AttributeValue) (types.TransactWriteItem, error) {
	item, err := attributevalue.MarshalMap(r)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	p := &types.Put{TableName: &s.Table, Item: item, ConditionExpression: &condition, ExpressionAttributeValues: values}
	if condition == "#version = :previous" {
		p.ExpressionAttributeNames = map[string]string{"#version": "version"}
	} else if strings.Contains(condition, "#status") {
		p.ExpressionAttributeNames = map[string]string{"#status": "status"}
	}
	return types.TransactWriteItem{Put: p}, nil
}

func fingerprint(command Command) (string, error) {
	b, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:]), nil
}

// Apply confirma estado, diario, deduplicación y outbox de comprobante en una única transacción.
// transition debe ser pura: NO llamar Khipu, enviar correo ni modificar estado compartido.
func (s Service) Apply(ctx context.Context, command Command, transition func(collection.Account) (collection.Change, error)) (collection.Change, error) {
	if command.ID == "" || command.AccountID == "" || command.ExpectedVersion < 1 || s.DB == nil || s.Table == "" || transition == nil {
		return collection.Change{}, collection.ErrInvalid
	}
	hash, err := fingerprint(command)
	if err != nil {
		return collection.Change{}, err
	}
	previous, err := s.read(ctx, "COMMAND#"+command.ID, "META")
	if err == nil {
		return replay(previous, hash)
	}
	if !errors.Is(err, ErrNotFound) {
		return collection.Change{}, err
	}
	a, err := s.GetAccount(ctx, command.AccountID)
	if err != nil {
		return collection.Change{}, err
	}
	if a.Version != command.ExpectedVersion {
		return collection.Change{}, collection.ErrConflict
	}
	c, err := transition(a)
	if err != nil {
		return collection.Change{}, err
	}
	if c.Account.ID != a.ID || c.Account.TripID != a.TripID || c.Account.ParticipantID != a.ParticipantID || c.Account.Version != a.Version+1 || c.Event.CommandID != command.ID || c.Event.AccountID != a.ID || c.Event.TripID != a.TripID || c.Account.Validate() != nil {
		return collection.Change{}, collection.ErrInvalid
	}
	if len(c.Event.Entries) > 0 && (command.Reference == "" || c.Event.Reference != command.Reference) && (c.Event.Type == "PAYMENT_RECEIVED" || c.Event.Type == "PAYMENT_REQUIRES_REVIEW" || c.Event.Type == "DEPOSIT_RECEIVED" || c.Event.Type == "REFUND_PAID") {
		return collection.Change{}, collection.ErrInvalid
	}
	rows := []record{
		{PK: "ACCOUNT#" + a.ID, SK: "META", Version: c.Account.Version, Account: &c.Account},
		{PK: "COMMAND#" + command.ID, SK: "META", Fingerprint: hash, Change: &c},
		{PK: "ACCOUNT#" + a.ID, SK: "EVENT#" + fmt.Sprintf("%020d", c.Account.Version), Event: &c.Event},
	}
	if command.Reference != "" {
		rows = append(rows, record{PK: "REFERENCE#" + command.Reference, SK: "META", Fingerprint: hash})
	}
	if command.Attempt != nil {
		if command.Attempt.ID == "" || command.Attempt.AccountID != a.ID || c.Account.OpenAttemptID != command.Attempt.ID || !validPortalID(command.PaymentSessionID) {
			return collection.Change{}, collection.ErrInvalid
		}
		_, expected, attemptErr := collection.OpenAttempt(a, collection.Audit{CommandID: c.Event.CommandID, Actor: c.Event.Actor, Reason: c.Event.Reason, RecordedAt: c.Event.RecordedAt}, command.Attempt.ID, command.Attempt.Email)
		if attemptErr != nil || expected != *command.Attempt || c.Event.Type != "PAYMENT_ATTEMPT_OPENED" || c.Event.Amount != expected.Amount || c.Event.Reference != expected.ID {
			return collection.Change{}, collection.ErrInvalid
		}
		rows = append(rows, record{PK: "ATTEMPT#" + command.Attempt.ID, SK: "META", Attempt: command.Attempt, PaymentSessionID: command.PaymentSessionID})
		rows = append(rows, record{PK: "ACCOUNT#" + a.ID, SK: "ATTEMPT#" + command.Attempt.ID, AccountID: a.ID, Attempt: command.Attempt})
	}
	if command.ReceiptID != "" {
		if command.Reference == "" || c.Event.Amount <= 0 || (c.Event.Type != "PAYMENT_RECEIVED" && c.Event.Type != "PAYMENT_REQUIRES_REVIEW" && c.Event.Type != "DEPOSIT_RECEIVED") {
			return collection.Change{}, collection.ErrInvalid
		}
		receipt := record{PK: "RECEIPT#" + command.ReceiptID, SK: "META", ReceiptID: command.ReceiptID, ReceiptEmail: command.ReceiptEmail, Event: &c.Event, Status: "PENDING_DOCUMENT"}
		if identity, identityErr := s.read(ctx, "TRIP#"+a.TripID, "MEMBER#"+a.ID); identityErr == nil && identity.Roster != nil && strings.TrimSpace(identity.Roster.Name) != "" && strings.TrimSpace(identity.Roster.Document) != "" {
			receipt.DocumentVersion, receipt.PassengerName, receipt.PassengerDocument = 3, strings.TrimSpace(identity.Roster.Name), strings.TrimSpace(identity.Roster.Document)
		}
		rows = append(rows, receipt)
		rows = append(rows, record{PK: "ACCOUNT#" + a.ID, SK: "RECEIPT#" + command.ReceiptID, ReceiptID: command.ReceiptID, AccountID: a.ID, Event: &collection.Event{Type: c.Event.Type, Amount: c.Event.Amount, EffectiveDate: c.Event.EffectiveDate}})
		rows = append(rows, record{PK: "JOB#" + c.Event.RecordedAt.UTC().Format("2006-01-02"), SK: "PENDING#" + command.ReceiptID, ReceiptID: command.ReceiptID, Status: "PENDING"})
	}
	if command.ConfirmedAttemptID != "" {
		if c.Event.AttemptID != command.ConfirmedAttemptID || command.Reference == "" || (c.Event.Type != "PAYMENT_RECEIVED" && c.Event.Type != "PAYMENT_REQUIRES_REVIEW") {
			return collection.Change{}, collection.ErrInvalid
		}
		// Referencia por intento: misma transacción que el diario, sin depender del resultado de creación.
		rows = append(rows, record{PK: "ATTEMPT#" + command.ConfirmedAttemptID, SK: "OUTCOME", AccountID: a.ID, Event: &c.Event})
	}
	if command.ResolvedAttemptID != "" {
		if c.Event.AttemptID != command.ResolvedAttemptID || (c.Event.Type != "PAYMENT_ATTEMPT_RESOLVED_UNPAID" && c.Event.Type != "PAYMENT_REVERSED_BY_PROVIDER") {
			return collection.Change{}, collection.ErrInvalid
		}
		rows = append(rows, record{PK: "ATTEMPT#" + command.ResolvedAttemptID, SK: "RESOLUTION", AccountID: a.ID, Event: &c.Event})
	}
	if (c.Event.Type == "PAYMENT_RECEIVED" || c.Event.Type == "PAYMENT_REQUIRES_REVIEW") && strings.HasPrefix(command.Reference, "khipu:") {
		rows = append(rows, record{PK: "TRIP#" + a.TripID, SK: "SETTLEMENT#" + command.Reference, TripID: a.TripID, Version: 1, Settlement: &collection.Settlement{PaymentReference: command.Reference, TripID: a.TripID, Amount: c.Event.Amount, Version: 1}})
	}
	if c.Event.Type == "PAYMENT_RECEIVED" && strings.HasPrefix(command.Reference, "khipu:") {
		if command.ReceiptID == "" {
			return collection.Change{}, collection.ErrInvalid
		}
		tax := newManualSalesReceiptRequest(command.ReceiptID, c.Event)
		rows = append(rows,
			record{PK: "TAX_REQUEST#" + tax.RequestID, SK: "META", TaxRequest: &tax, Status: tax.Status, AccountID: tax.AccountID, TripID: tax.TripID},
			record{PK: "TAX_QUEUE#PENDING", SK: tax.RequestedAt.UTC().Format(time.RFC3339Nano) + "#" + tax.RequestID, TaxRequest: &tax, Status: tax.Status, AccountID: tax.AccountID, TripID: tax.TripID},
		)
	}
	rows = append(rows, cashProjectionRecords(c.Event)...)
	// La lectura previa no basta: un anexo puede bloquear el plan antes del commit.
	// Esta condición participa en la misma transacción que el saldo y el diario.
	writes := make([]types.TransactWriteItem, 0, len(rows)+1)
	writes = append(writes, s.activePlanCheck(a.TripID))
	for i, r := range rows {
		condition := "attribute_not_exists(pk)"
		var values map[string]types.AttributeValue
		if i == 0 {
			condition = "#version = :previous"
			values = map[string]types.AttributeValue{":previous": &types.AttributeValueMemberN{Value: strconv.FormatInt(a.Version, 10)}}
		}
		if r.SK == "OUTCOME" && command.ConfirmedAttemptID != "" {
			condition = "attribute_not_exists(pk) OR accountId = :accountId"
			values = map[string]types.AttributeValue{":accountId": &types.AttributeValueMemberS{Value: a.ID}}
		}
		if r.SK == "RESOLUTION" && command.ResolvedAttemptID != "" {
			condition = "attribute_not_exists(pk) OR accountId = :accountId"
			values = map[string]types.AttributeValue{":accountId": &types.AttributeValueMemberS{Value: a.ID}}
		}
		write, err := s.put(r, condition, values)
		if err != nil {
			return collection.Change{}, err
		}
		writes = append(writes, write)
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err == nil {
		return c, nil
	}
	// Una respuesta perdida puede ocultar un commit exitoso: consultar el comando antes de reintentar.
	if stored, readErr := s.read(ctx, "COMMAND#"+command.ID, "META"); readErr == nil {
		return replay(stored, hash)
	}
	var cancelled *types.TransactionCanceledException
	if errors.As(err, &cancelled) {
		if command.Reference != "" {
			if _, readErr := s.read(ctx, "REFERENCE#"+command.Reference, "META"); readErr == nil {
				return collection.Change{}, ErrReferenceUsed
			}
		}
		return collection.Change{}, collection.ErrConflict
	}
	return collection.Change{}, fmt.Errorf("confirmar operacion: %w", err)
}

func replay(r record, hash string) (collection.Change, error) {
	if r.Fingerprint != hash {
		return collection.Change{}, ErrReplayMismatch
	}
	if r.Change == nil {
		return collection.Change{}, collection.ErrInvalid
	}
	return *r.Change, nil
}
