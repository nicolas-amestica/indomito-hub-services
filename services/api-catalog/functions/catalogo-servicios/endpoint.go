// Package servicecatalog administra el catalogo de servicios de cotizacion.
package servicecatalog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/labstack/echo/v4"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions"
)

var serviceIDPattern = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

type scopePointer struct {
	PK        string `dynamodbav:"pk"`
	SK        string `dynamodbav:"sk"`
	CatalogPK string `dynamodbav:"catalogPk"`
}

// Service es el contrato persistido y expuesto por la API.
type Service struct {
	PK          string  `json:"-" dynamodbav:"pk"`
	SK          string  `json:"-" dynamodbav:"sk"`
	ID          string  `json:"id" dynamodbav:"id"`
	Scope       string  `json:"scope" dynamodbav:"scope"`
	Glosa       string  `json:"glosa" dynamodbav:"glosa"`
	Description string  `json:"description,omitempty" dynamodbav:"description,omitempty"`
	Price       float64 `json:"price" dynamodbav:"price"`
	Currency    string  `json:"currency" dynamodbav:"currency"`
	ChargeType  string  `json:"chargeType" dynamodbav:"chargeType"`
	Active      bool    `json:"active" dynamodbav:"active"`
	Default     bool    `json:"default" dynamodbav:"default"`
	UpdatedAt   string  `json:"updatedAt,omitempty" dynamodbav:"updatedAt,omitempty"`
}

type serviceInput struct {
	Glosa       string  `json:"glosa" validate:"required,max=160"`
	Description string  `json:"description" validate:"max=300"`
	Price       float64 `json:"price" validate:"gte=0,lte=99999999"`
	Currency    string  `json:"currency" validate:"required,oneof=CLP USD BRL"`
	ChargeType  string  `json:"chargeType" validate:"required,oneof=fixed per_passenger per_passenger_night per_day per_passenger_day"`
	Active      bool    `json:"active"`
	Default     bool    `json:"default"`
}

type listResponse struct {
	Items []Service `json:"items"`
}

func Register(e *echo.Echo, app *functions.App, _ *zap.Logger) {
	functions.ServiceCatalogListRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return list(ctx, app, req)
	}))
	functions.ServiceCatalogCreateRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return create(ctx, app, req)
	}))
	functions.ServiceCatalogUpdateRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return update(ctx, app, req)
	}))
}

func HandleList(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return list(ctx, app, req)
}

func HandleCreate(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return create(ctx, app, req)
}

func HandleUpdate(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return update(ctx, app, req)
}

func list(ctx context.Context, app *functions.App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	scope, err := validatedScope(req.QueryStringParameters["scope"])
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	includeInactive, _ := strconv.ParseBool(req.QueryStringParameters["includeInactive"])
	if includeInactive && !isAdministrator(req) {
		return lambdautil.ErrorResponse(req, apperr.Forbidden("Solo administracion puede consultar servicios inactivos"))
	}
	items, err := queryServices(ctx, app, scope, includeInactive)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(http.StatusOK, listResponse{Items: items}, map[string]string{"Cache-Control": "private, max-age=300"})
}

func create(ctx context.Context, app *functions.App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdministrator(req) {
		return lambdautil.ErrorResponse(req, apperr.Forbidden("Solo administracion puede crear servicios"))
	}
	scope, err := validatedScope(req.QueryStringParameters["scope"])
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	input, err := bindServiceInput(req)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	catalogPK, err := resolveCatalogPK(ctx, app, scope)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	id := ulid.Make().String()
	item := serviceFromInput(catalogPK, scope, id, input)
	encoded, err := attributevalue.MarshalMap(item)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	_, err = app.DDB.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(app.Config.CatalogsTableName), Item: encoded, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")})
	if err != nil {
		var conditional *types.ConditionalCheckFailedException
		if errors.As(err, &conditional) {
			return lambdautil.ErrorResponse(req, apperr.Conflict("El identificador generado ya existe; reintenta la creación"))
		}
		return lambdautil.ErrorResponse(req, err)
	}
	return lambdautil.SuccessResponse(http.StatusCreated, item)
}

func update(ctx context.Context, app *functions.App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdministrator(req) {
		return lambdautil.ErrorResponse(req, apperr.Forbidden("Solo administracion puede editar servicios"))
	}
	scope, err := validatedScope(req.QueryStringParameters["scope"])
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	id := strings.ToUpper(strings.TrimSpace(req.PathParameters["id"]))
	if !serviceIDPattern.MatchString(id) {
		return lambdautil.ErrorResponse(req, apperr.Validation("El identificador del servicio no es valido"))
	}
	input, err := bindServiceInput(req)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	catalogPK, err := resolveCatalogPK(ctx, app, scope)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	item := serviceFromInput(catalogPK, scope, id, input)
	encoded, err := attributevalue.MarshalMap(item)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	_, err = app.DDB.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(app.Config.CatalogsTableName), Item: encoded, ConditionExpression: aws.String("attribute_exists(pk) AND attribute_exists(sk)")})
	if err != nil {
		var conditional *types.ConditionalCheckFailedException
		if errors.As(err, &conditional) {
			return lambdautil.ErrorResponse(req, apperr.NotFound("El servicio solicitado no existe"))
		}
		return lambdautil.ErrorResponse(req, err)
	}
	return lambdautil.SuccessResponse(http.StatusOK, item)
}

func bindServiceInput(req events.APIGatewayV2HTTPRequest) (serviceInput, error) {
	var input serviceInput
	if err := lambdautil.BindJSON(req, &input); err != nil {
		return input, err
	}
	input.Glosa = strings.TrimSpace(input.Glosa)
	input.Description = strings.TrimSpace(input.Description)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.ChargeType = strings.ToLower(strings.TrimSpace(input.ChargeType))
	if err := lambdautil.ValidateStruct(input); err != nil {
		return input, err
	}
	return input, nil
}

func validatedScope(raw string) (string, error) {
	scope := strings.ToUpper(strings.TrimSpace(raw))
	if scope != "CTZ" {
		return "", apperr.InvalidQueryParameter("El scope del catalogo de servicios no es valido")
	}
	return scope, nil
}

func resolveCatalogPK(ctx context.Context, app *functions.App, scope string) (string, error) {
	key, _ := attributevalue.MarshalMap(map[string]string{"pk": "CAT#SRV#SCOPE#" + scope, "sk": "META"})
	result, err := app.DDB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(app.Config.CatalogsTableName), Key: key, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return "", err
	}
	if len(result.Item) == 0 {
		return "", apperr.NotFound("No existe un catalogo de servicios para el scope solicitado")
	}
	var pointer scopePointer
	if err = attributevalue.UnmarshalMap(result.Item, &pointer); err != nil {
		return "", fmt.Errorf("decodificar relacion de catalogo: %w", err)
	}
	if !strings.HasPrefix(pointer.CatalogPK, "CAT#SRV#") {
		return "", fmt.Errorf("relacion de catalogo invalida")
	}
	return pointer.CatalogPK, nil
}

func queryServices(ctx context.Context, app *functions.App, scope string, includeInactive bool) ([]Service, error) {
	catalogPK, err := resolveCatalogPK(ctx, app, scope)
	if err != nil {
		return nil, err
	}
	items := make([]Service, 0)
	var startKey map[string]types.AttributeValue
	for {
		result, queryErr := app.DDB.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(app.Config.CatalogsTableName),
			KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk":     &types.AttributeValueMemberS{Value: catalogPK},
				":prefix": &types.AttributeValueMemberS{Value: "FRM#SCP#" + scope + "#"},
			},
			ExclusiveStartKey: startKey,
		})
		if queryErr != nil {
			return nil, queryErr
		}
		for _, raw := range result.Items {
			var item Service
			if err = attributevalue.UnmarshalMap(raw, &item); err != nil {
				return nil, fmt.Errorf("decodificar servicio: %w", err)
			}
			item.Currency = strings.ToUpper(item.Currency)
			if item.Scope != scope || !serviceIDPattern.MatchString(item.ID) || (!includeInactive && !item.Active) {
				continue
			}
			items = append(items, item)
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		startKey = result.LastEvaluatedKey
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Glosa) < strings.ToLower(items[j].Glosa) })
	return items, nil
}

func serviceFromInput(catalogPK, scope, id string, input serviceInput) Service {
	return Service{PK: catalogPK, SK: "FRM#SCP#" + scope + "#" + id, ID: id, Scope: scope, Glosa: input.Glosa, Description: input.Description, Price: input.Price, Currency: input.Currency, ChargeType: input.ChargeType, Active: input.Active, Default: input.Default, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
}

func isAdministrator(req events.APIGatewayV2HTTPRequest) bool {
	if req.RequestContext.Authorizer == nil {
		return false
	}
	value, ok := req.RequestContext.Authorizer.Lambda["profileCode"].(string)
	if !ok {
		return false
	}
	code := strings.ToUpper(strings.TrimSpace(value))
	return code == "ADMIN" || code == "ADM"
}
