package meetinghosts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
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

const (
	configurationPK = "CONFIGURATION"
	hostPrefix      = "MEET_HOST#"
	defaultHostSK   = "MEET_HOST_DEFAULT"
)

var hostIDPattern = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

type Host struct {
	PK          string `json:"-" dynamodbav:"pk"`
	SK          string `json:"sk" dynamodbav:"sk"`
	ID          string `json:"id" dynamodbav:"id"`
	Name        string `json:"name" dynamodbav:"name"`
	Description string `json:"description,omitempty" dynamodbav:"description,omitempty"`
	Color       string `json:"color" dynamodbav:"color"`
	Active      bool   `json:"active" dynamodbav:"active"`
	IsDefault   bool   `json:"isDefault" dynamodbav:"-"`
	CreatedAt   string `json:"createdAt" dynamodbav:"createdAt"`
	UpdatedAt   string `json:"updatedAt" dynamodbav:"updatedAt"`
}

type hostInput struct {
	Name        string `json:"name" validate:"required,max=120"`
	Description string `json:"description" validate:"max=300"`
	Color       string `json:"color" validate:"required,hexcolor"`
	Active      bool   `json:"active"`
	IsDefault   bool   `json:"isDefault"`
}

type defaultPointer struct {
	PK     string `dynamodbav:"pk"`
	SK     string `dynamodbav:"sk"`
	HostID string `dynamodbav:"hostId"`
}

type listResponse struct {
	Items []Host `json:"items"`
}

func Register(e *echo.Echo, app *functions.App, _ *zap.Logger) {
	functions.MeetingHostOptionsRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return options(ctx, app, req)
	}))
	functions.MeetingHostListRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return list(ctx, app, req)
	}))
	functions.MeetingHostCreateRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return create(ctx, app, req)
	}))
	functions.MeetingHostUpdateRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return update(ctx, app, req)
	}))
	functions.MeetingHostDeleteRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return remove(ctx, app, req)
	}))
}

func HandleList(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return list(ctx, app, req)
}

func HandleOptions(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return options(ctx, app, req)
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

func HandleDelete(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return remove(ctx, app, req)
}

func list(ctx context.Context, app *functions.App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	hosts, err := queryHosts(ctx, app)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	defaultID, err := getDefaultHostID(ctx, app)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	for index := range hosts {
		hosts[index].IsDefault = hosts[index].ID == defaultID
	}
	return lambdautil.SuccessResponse(http.StatusOK, listResponse{Items: hosts})
}

func options(ctx context.Context, app *functions.App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	hosts, err := queryHosts(ctx, app)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	defaultID, err := getDefaultHostID(ctx, app)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	active := make([]Host, 0, len(hosts))
	for _, host := range hosts {
		if host.Active {
			host.IsDefault = host.ID == defaultID
			active = append(active, host)
		}
	}
	return lambdautil.SuccessResponse(http.StatusOK, listResponse{Items: active})
}

func create(ctx context.Context, app *functions.App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdministrator(req) {
		return lambdautil.ErrorResponse(req, apperr.Forbidden("Solo administracion puede crear anfitriones"))
	}
	input, err := bindInput(req)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	host := hostFromInput(ulid.Make().String(), input, now, now)
	if err = putHost(ctx, app, host, true); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	if input.IsDefault {
		if err = setDefaultHost(ctx, app, host.ID); err != nil {
			return lambdautil.ErrorResponse(req, err)
		}
	}
	host.IsDefault = input.IsDefault
	return lambdautil.SuccessResponse(http.StatusCreated, host)
}

func update(ctx context.Context, app *functions.App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdministrator(req) {
		return lambdautil.ErrorResponse(req, apperr.Forbidden("Solo administracion puede editar anfitriones"))
	}
	id, err := validatedID(req.PathParameters["id"])
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	input, err := bindInput(req)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	existing, err := getHost(ctx, app, id)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	host := hostFromInput(id, input, existing.CreatedAt, time.Now().UTC().Format(time.RFC3339))
	if err = putHost(ctx, app, host, false); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	if input.IsDefault {
		err = setDefaultHost(ctx, app, id)
	} else {
		err = clearDefaultHost(ctx, app, id)
	}
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	host.IsDefault = input.IsDefault
	return lambdautil.SuccessResponse(http.StatusOK, host)
}

func remove(ctx context.Context, app *functions.App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdministrator(req) {
		return lambdautil.ErrorResponse(req, apperr.Forbidden("Solo administracion puede eliminar anfitriones"))
	}
	id, err := validatedID(req.PathParameters["id"])
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	_, err = app.DDB.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(app.Config.ConfigurationsTableName), Key: hostKey(id),
		ConditionExpression: aws.String("attribute_exists(pk) AND attribute_exists(sk)"),
	})
	if err != nil {
		var conditional *types.ConditionalCheckFailedException
		if errors.As(err, &conditional) {
			return lambdautil.ErrorResponse(req, apperr.NotFound("El anfitrion solicitado no existe"))
		}
		return lambdautil.ErrorResponse(req, err)
	}
	if err = clearDefaultHost(ctx, app, id); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return lambdautil.SuccessResponse(http.StatusOK, map[string]bool{"deleted": true})
}

func bindInput(req events.APIGatewayV2HTTPRequest) (hostInput, error) {
	var input hostInput
	if err := lambdautil.BindJSON(req, &input); err != nil {
		return input, err
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Color = strings.ToUpper(strings.TrimSpace(input.Color))
	if input.IsDefault && !input.Active {
		return input, apperr.Validation("El anfitrion predeterminado debe estar activo")
	}
	if err := lambdautil.ValidateStruct(input); err != nil {
		return input, err
	}
	return input, nil
}

func hostFromInput(id string, input hostInput, createdAt, updatedAt string) Host {
	return Host{PK: configurationPK, SK: hostPrefix + id, ID: id, Name: input.Name, Description: input.Description, Color: input.Color, Active: input.Active, CreatedAt: createdAt, UpdatedAt: updatedAt}
}

func putHost(ctx context.Context, app *functions.App, host Host, create bool) error {
	item, err := attributevalue.MarshalMap(host)
	if err != nil {
		return err
	}
	condition := "attribute_exists(pk) AND attribute_exists(sk)"
	if create {
		condition = "attribute_not_exists(pk) AND attribute_not_exists(sk)"
	}
	_, err = app.DDB.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(app.Config.ConfigurationsTableName), Item: item, ConditionExpression: aws.String(condition)})
	if err != nil {
		var conditional *types.ConditionalCheckFailedException
		if errors.As(err, &conditional) {
			if create {
				return apperr.Conflict("El identificador generado ya existe; reintenta la creacion")
			}
			return apperr.NotFound("El anfitrion solicitado no existe")
		}
	}
	return err
}

func queryHosts(ctx context.Context, app *functions.App) ([]Host, error) {
	hosts := make([]Host, 0)
	var startKey map[string]types.AttributeValue
	for {
		result, err := app.DDB.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(app.Config.ConfigurationsTableName),
			KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: configurationPK}, ":prefix": &types.AttributeValueMemberS{Value: hostPrefix},
			}, ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, err
		}
		for _, item := range result.Items {
			var host Host
			if err = attributevalue.UnmarshalMap(item, &host); err != nil {
				return nil, fmt.Errorf("decodificar anfitrion: %w", err)
			}
			hosts = append(hosts, host)
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		startKey = result.LastEvaluatedKey
	}
	sort.Slice(hosts, func(i, j int) bool { return strings.ToLower(hosts[i].Name) < strings.ToLower(hosts[j].Name) })
	return hosts, nil
}

func getHost(ctx context.Context, app *functions.App, id string) (Host, error) {
	result, err := app.DDB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(app.Config.ConfigurationsTableName), Key: hostKey(id), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return Host{}, err
	}
	if len(result.Item) == 0 {
		return Host{}, apperr.NotFound("El anfitrion solicitado no existe")
	}
	var host Host
	if err = attributevalue.UnmarshalMap(result.Item, &host); err != nil {
		return Host{}, err
	}
	return host, nil
}

func getDefaultHostID(ctx context.Context, app *functions.App) (string, error) {
	result, err := app.DDB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(app.Config.ConfigurationsTableName), Key: defaultKey(), ConsistentRead: aws.Bool(true)})
	if err != nil || len(result.Item) == 0 {
		return "", err
	}
	var pointer defaultPointer
	if err = attributevalue.UnmarshalMap(result.Item, &pointer); err != nil {
		return "", err
	}
	return pointer.HostID, nil
}

func setDefaultHost(ctx context.Context, app *functions.App, id string) error {
	item, err := attributevalue.MarshalMap(defaultPointer{PK: configurationPK, SK: defaultHostSK, HostID: id})
	if err != nil {
		return err
	}
	_, err = app.DDB.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(app.Config.ConfigurationsTableName), Item: item})
	return err
}

func clearDefaultHost(ctx context.Context, app *functions.App, id string) error {
	_, err := app.DDB.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(app.Config.ConfigurationsTableName), Key: defaultKey(),
		ConditionExpression:       aws.String("hostId = :hostId"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":hostId": &types.AttributeValueMemberS{Value: id}},
	})
	var conditional *types.ConditionalCheckFailedException
	if errors.As(err, &conditional) {
		return nil
	}
	return err
}

func validatedID(raw string) (string, error) {
	id := strings.ToUpper(strings.TrimSpace(raw))
	if !hostIDPattern.MatchString(id) {
		return "", apperr.Validation("El identificador del anfitrion no es valido")
	}
	return id, nil
}

func hostKey(id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: configurationPK}, "sk": &types.AttributeValueMemberS{Value: hostPrefix + id}}
}

func defaultKey() map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: configurationPK}, "sk": &types.AttributeValueMemberS{Value: defaultHostSK}}
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
