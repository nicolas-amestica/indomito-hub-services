package dteinbox

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Database limita operaciones a claves explícitas, sin Scan.
type Database interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

// AcquireLease serializa el cursor sin depender de concurrencia reservada Lambda.
// Su vigencia supera el timeout máximo de la función (300 segundos).
func (s Store) AcquireLease(ctx context.Context, owner string, now time.Time) (bool, error) {
	item := keys("DTE_INBOX#LEASE", "META")
	item["owner"] = &types.AttributeValueMemberS{Value: owner}
	item["expiresAt"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(now.Add(6*time.Minute).Unix(), 10)}
	_, err := s.DB.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.Table, Item: item, ConditionExpression: aws.String("attribute_not_exists(pk) OR expiresAt < :now"), ExpressionAttributeValues: map[string]types.AttributeValue{":now": &types.AttributeValueMemberN{Value: strconv.FormatInt(now.Unix(), 10)}}})
	var conflict *types.ConditionalCheckFailedException
	if errors.As(err, &conflict) {
		return false, nil
	}
	return err == nil, err
}

// ReleaseLease solo elimina el bloqueo que pertenece a esta ejecución.
func (s Store) ReleaseLease(ctx context.Context, owner string) error {
	_, err := s.DB.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: &s.Table, Key: keys("DTE_INBOX#LEASE", "META"), ConditionExpression: aws.String("#owner = :owner"), ExpressionAttributeNames: map[string]string{"#owner": "owner"}, ExpressionAttributeValues: map[string]types.AttributeValue{":owner": &types.AttributeValueMemberS{Value: owner}}})
	return err
}

var ErrDocumentConflict = errors.New("XML diferente para identidad existente")

// Store separa las particiones del receptor de las del emisor de boletas.
type Store struct {
	DB    Database
	Table string
}

type row struct {
	Request   *SyncRequest `dynamodbav:"request,omitempty"`
	RequestID string       `dynamodbav:"requestId,omitempty"`
	PK        string       `dynamodbav:"pk"`
	SK        string       `dynamodbav:"sk"`
	Document  *Document    `dynamodbav:"document,omitempty"`
	State     *SyncState   `dynamodbav:"state,omitempty"`
	Status    string       `dynamodbav:"status,omitempty"`
	Detail    string       `dynamodbav:"detail,omitempty"`
}

// SyncState mantiene paginación durable y el último barrido terminado.
type SyncState struct {
	After        string `json:"after"`
	Page         string `json:"page"`
	StartedAt    string `json:"startedAt"`
	LastSuccess  string `json:"lastSuccess"`
	ReconciledAt string `json:"reconciledAt"`
}

func keys(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}
}
func (s Store) read(ctx context.Context, pk, sk string) (row, error) {
	out, err := s.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.Table, Key: keys(pk, sk), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return row{}, err
	}
	var item row
	err = attributevalue.UnmarshalMap(out.Item, &item)
	return item, err
}
func (s Store) write(ctx context.Context, item row, unique bool) error {
	attrs, err := attributevalue.MarshalMap(item)
	if err != nil {
		return err
	}
	input := &dynamodb.PutItemInput{TableName: &s.Table, Item: attrs}
	if unique {
		input.ConditionExpression = aws.String("attribute_not_exists(pk)")
	}
	_, err = s.DB.PutItem(ctx, input)
	return err
}

// State consulta el cursor propio del ambiente.
func (s Store) State(ctx context.Context) (SyncState, error) {
	r, e := s.read(ctx, "DTE_INBOX#SYNC", "META")
	if r.State == nil {
		return SyncState{}, e
	}
	return *r.State, e
}

// SaveState avanza solo después de persistir todos los mensajes de la página.
func (s Store) SaveState(ctx context.Context, state SyncState) error {
	return s.write(ctx, row{PK: "DTE_INBOX#SYNC", SK: "META", State: &state}, false)
}

// Seen consulta el resultado durable de un mensaje.
func (s Store) Seen(ctx context.Context, id string) (bool, error) {
	r, e := s.read(ctx, "DTE_MAIL#"+id, "META")
	return r.Status != "", e
}

// Mark conserva fallos en cuarentena sin bloquear los correos siguientes.
func (s Store) Mark(ctx context.Context, id, status, detail string) error {
	if status == "QUARANTINED" || status == "GONE" || status == "OVERSIZED" {
		if err := s.write(ctx, row{PK: "DTE_INBOX#ISSUES", SK: id, Status: status, Detail: detail}, false); err != nil {
			return err
		}
	}
	return s.write(ctx, row{PK: "DTE_MAIL#" + id, SK: "META", Status: status, Detail: detail}, false)
}

// Issue informa correos que requieren intervención sin exponer su contenido.
type Issue struct {
	MessageID string `json:"messageId"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
}

// Issues muestra hasta cincuenta incidencias pendientes de revisión.
func (s Store) Issues(ctx context.Context) ([]Issue, error) {
	out, err := s.DB.Query(ctx, &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "DTE_INBOX#ISSUES"}}, Limit: aws.Int32(50), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return nil, err
	}
	issues := []Issue{}
	for _, attrs := range out.Items {
		var r row
		if err = attributevalue.UnmarshalMap(attrs, &r); err != nil {
			return nil, err
		}
		issues = append(issues, Issue{MessageID: r.SK, Status: r.Status, Detail: r.Detail})
	}
	return issues, nil
}

// SaveDocument deduplica por identidad y detecta sobres conflictivos sin sobrescribir.
func (s Store) SaveDocument(ctx context.Context, document Document) error {
	global, err := s.read(ctx, "DTE_RECEIVED_ID#"+document.ID, "META")
	if err != nil {
		return err
	}
	if global.Document != nil {
		if global.Document.SHA256 != document.SHA256 {
			return ErrDocumentConflict
		}
		return nil
	}
	identity, err := attributevalue.MarshalMap(row{PK: "DTE_RECEIVED_ID#" + document.ID, SK: "META", Document: &document})
	if err != nil {
		return err
	}
	projection, err := attributevalue.MarshalMap(row{PK: "DTE_RECEIVED#" + document.IssueDate[:7], SK: document.ID, Document: &document})
	if err != nil {
		return err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Put: &types.Put{TableName: &s.Table, Item: identity, ConditionExpression: aws.String("attribute_not_exists(pk)")}}, {Put: &types.Put{TableName: &s.Table, Item: projection, ConditionExpression: aws.String("attribute_not_exists(pk)")}}}})
	return err
}

// Get obtiene un documento por mes e identidad.
func (s Store) Get(ctx context.Context, month, id string) (Document, error) {
	r, e := s.read(ctx, "DTE_RECEIVED#"+month, id)
	if e != nil {
		return Document{}, e
	}
	if r.Document == nil {
		return Document{}, errors.New("DTE no encontrado")
	}
	return *r.Document, nil
}

// Update actualiza la consulta oficial sin modificar el original.
func (s Store) Update(ctx context.Context, d Document) error {
	return s.write(ctx, row{PK: "DTE_RECEIVED#" + d.IssueDate[:7], SK: d.ID, Document: &d}, false)
}

// List consulta un mes sin recorrer la tabla completa.
func (s Store) List(ctx context.Context, month, cursor string) ([]Document, string, error) {
	input := &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "DTE_RECEIVED#" + month}}, Limit: aws.Int32(50), ConsistentRead: aws.Bool(true)}
	if cursor != "" {
		input.ExclusiveStartKey = keys("DTE_RECEIVED#"+month, cursor)
	}
	out, err := s.DB.Query(ctx, input)
	if err != nil {
		return nil, "", err
	}
	items := []Document{}
	for _, attrs := range out.Items {
		var item row
		if err = attributevalue.UnmarshalMap(attrs, &item); err != nil {
			return nil, "", err
		}
		if item.Document != nil {
			items = append(items, *item.Document)
		}
	}
	next := ""
	if sk, ok := out.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
		next = sk.Value
	}
	return items, strings.TrimSpace(next), nil
}

// ClaimAction audita antes del envío. Un resultado incierto requiere consulta,
// nunca reenvío automático del mismo comando tributario.
func (s Store) ClaimAction(ctx context.Context, id, command, actor, action, reason string) error {
	detail, err := json.Marshal(map[string]string{"actor": actor, "action": action, "reason": reason, "at": time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	return s.write(ctx, row{PK: "DTE_ACTION#" + id, SK: command, Status: "REQUESTED", Detail: string(detail)}, true)
}

// FinishAction conserva el resultado oficial junto a la identidad del operador.
func (s Store) FinishAction(ctx context.Context, id, command, actor, action, reason string, result any) error {
	detail, err := json.Marshal(map[string]any{"actor": actor, "action": action, "reason": reason, "result": result, "at": time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	return s.write(ctx, row{PK: "DTE_ACTION#" + id, SK: command, Status: "COMPLETED", Detail: string(detail)}, false)
}
