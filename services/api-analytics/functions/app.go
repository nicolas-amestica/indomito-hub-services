// Package functions implementa la planificación analítica y consume contratos de lectura operativa.
package functions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Database permite comprobar consultas y transacciones sin AWS.
type Database interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}
type queryDatabase interface {
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// Service conserva las tablas explícitas: Table es planificación; PaymentsTable es solo lectura.
type Service struct {
	DB            Database
	Table         string
	PaymentsTable string
}

// AdminApp comparte cliente y separa persistencia analítica de fuentes contractuales.
type AdminApp struct {
	Accounts  Service
	Contracts Service
}

var appMu sync.Mutex
var appInstance *AdminApp

// ErrNotFound indica una fuente o revisión inexistente.
var ErrNotFound = errors.New("recurso no encontrado")

// ErrReplayMismatch indica el uso de un comando con otra intención.
var ErrReplayMismatch = errors.New("comando reutilizado con otra intención")

func getAdminApp(ctx context.Context) (*AdminApp, error) {
	appMu.Lock()
	defer appMu.Unlock()
	if appInstance != nil {
		return appInstance, nil
	}
	programs, payments := os.Getenv("PROGRAMS_TABLE_NAME"), os.Getenv("PAYMENTS_TABLE_NAME")
	if programs == "" || payments == "" {
		return nil, errors.New("configuración analítica incompleta")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return nil, err
	}
	db := dynamodb.NewFromConfig(cfg)
	appInstance = &AdminApp{Accounts: Service{DB: db, Table: programs, PaymentsTable: payments}, Contracts: Service{DB: db, Table: programs, PaymentsTable: payments}}
	return appInstance, nil
}
func validPortalID(value string) bool { _, err := ulid.ParseStrict(value); return err == nil }
func key(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}
}
func decodeAdmin(req events.APIGatewayV2HTTPRequest, target interface{}) error {
	body := []byte(req.Body)
	if req.IsBase64Encoded {
		var err error
		body, err = base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return domain.ErrInvalid
		}
	}
	if len(body) > 190000 {
		return domain.ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(interface{})) != io.EOF {
		return domain.ErrInvalid
	}
	return nil
}
func adminFailure(req events.APIGatewayV2HTTPRequest, status int, reason string) (events.APIGatewayV2HTTPResponse, error) {
	code := map[int]string{400: apperr.CodeValidationError, 401: apperr.CodeUnauthorized, 403: apperr.CodeForbidden, 404: apperr.CodeResourceNotFound, 409: apperr.CodeConflict, 503: apperr.CodeUpstreamServiceError}[status]
	if code == "" {
		code = apperr.CodeInternalError
	}
	response, err := lambdautil.ErrorResponse(req, apperr.New(code, "No fue posible completar la operación de analítica."))
	if response.Headers == nil {
		response.Headers = map[string]string{}
	}
	response.Headers["cache-control"] = "no-store"
	return response, err
}
func domainFailure(req events.APIGatewayV2HTTPRequest, err error) (events.APIGatewayV2HTTPResponse, error) {
	switch {
	case errors.Is(err, ErrNotFound):
		return adminFailure(req, 404, "NOT_FOUND")
	case errors.Is(err, ErrReplayMismatch), errors.Is(err, domain.ErrConflict):
		return adminFailure(req, 409, "CONFLICT")
	case errors.Is(err, domain.ErrInvalid):
		return adminFailure(req, 400, "INVALID")
	default:
		return adminFailure(req, 503, "UNAVAILABLE")
	}
}
func monthKeys(from, to time.Time) []string {
	current := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, time.UTC)
	months := []string{}
	for !current.After(last) && len(months) <= 12 {
		months = append(months, current.Format("2006-01"))
		current = current.AddDate(0, 1, 0)
	}
	return months
}

type record struct {
	Version             int64                      `dynamodbav:"version"`
	Status              string                     `dynamodbav:"status"`
	RosterSchemaVersion int                        `dynamodbav:"rosterSchemaVersion"`
	Account             *domain.Account            `dynamodbav:"account"`
	Supplier            *domain.SupplierCommitment `dynamodbav:"supplier"`
	Event               *domain.Event              `dynamodbav:"event"`
	Settlement          *domain.Settlement         `dynamodbav:"settlement"`
	Roster              *Roster                    `dynamodbav:"roster"`
}

// Roster es la proyección mínima de identidad de cuenta, sin datos personales.
type Roster struct {
	AccountID string `dynamodbav:"accountId"`
}

func (s Service) read(ctx context.Context, pk, sk string) (record, error) {
	out, err := s.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.PaymentsTable, Key: key(pk, sk), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return record{}, err
	}
	if len(out.Item) == 0 {
		return record{}, ErrNotFound
	}
	var row record
	err = attributevalue.UnmarshalMap(out.Item, &row)
	return row, err
}
func (s Service) paymentRows(ctx context.Context, tripID, prefix, cursor string) ([]record, string, error) {
	input := &dynamodb.QueryInput{TableName: &s.PaymentsTable, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "TRIP#" + tripID}, ":prefix": &types.AttributeValueMemberS{Value: prefix}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(50)}
	if cursor != "" {
		input.ExclusiveStartKey = key("TRIP#"+tripID, cursor)
	}
	out, err := s.DB.Query(ctx, input)
	if err != nil {
		return nil, "", err
	}
	rows := []record{}
	for _, item := range out.Items {
		var row record
		if err = attributevalue.UnmarshalMap(item, &row); err != nil {
			return nil, "", err
		}
		rows = append(rows, row)
	}
	next := ""
	if value, ok := out.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
		next = value.Value
	}
	return rows, next, nil
}

// GetAccount reutiliza la proyección y reglas del dominio de pagos por clave exacta.
func (s Service) GetAccount(ctx context.Context, id string) (domain.Account, error) {
	row, err := s.read(ctx, "ACCOUNT#"+id, "META")
	if err != nil {
		return domain.Account{}, err
	}
	if row.Account == nil || row.Version != row.Account.Version {
		return domain.Account{}, domain.ErrInvalid
	}
	return *row.Account, row.Account.Validate()
}

type rosterPage struct {
	Items      []Roster
	NextCursor string
}

func (s Service) ListRoster(ctx context.Context, id, cursor string) (rosterPage, error) {
	plan, err := s.read(ctx, "TRIP#"+id, "META")
	if err != nil {
		return rosterPage{}, err
	}
	if plan.Status != "ACTIVE" || plan.RosterSchemaVersion != 1 {
		return rosterPage{}, domain.ErrConflict
	}

	rows, next, err := s.paymentRows(ctx, id, "MEMBER#", cursor)
	page := rosterPage{NextCursor: next}
	for _, row := range rows {
		if row.Roster == nil {
			return page, domain.ErrInvalid
		}
		page.Items = append(page.Items, *row.Roster)
	}
	return page, err
}

type supplierPage struct {
	Items      []domain.SupplierCommitment
	NextCursor string
}

func (s Service) ListSuppliers(ctx context.Context, id, cursor string) (supplierPage, error) {
	rows, next, err := s.paymentRows(ctx, id, "SUPPLIER#", cursor)
	page := supplierPage{NextCursor: next}
	for _, row := range rows {
		if row.Supplier == nil {
			return page, domain.ErrInvalid
		}
		page.Items = append(page.Items, *row.Supplier)
	}
	return page, err
}

type settlementPage struct {
	Items      []domain.Settlement
	NextCursor string
}

func (s Service) ListSettlements(ctx context.Context, id, cursor string) (settlementPage, error) {
	rows, next, err := s.paymentRows(ctx, id, "SETTLEMENT#", cursor)
	page := settlementPage{NextCursor: next}
	for _, row := range rows {
		if row.Settlement == nil {
			return page, domain.ErrInvalid
		}
		page.Items = append(page.Items, *row.Settlement)
	}
	return page, err
}

type contractAlertSummary struct {
	ID     string `dynamodbav:"id"`
	Status string `dynamodbav:"status"`
}
