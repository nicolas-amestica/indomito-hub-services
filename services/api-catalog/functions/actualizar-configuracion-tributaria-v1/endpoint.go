package taxsettings

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions"
)

type taxSettingsItem struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`
	program.TaxSettings
	UpdatedAt string `dynamodbav:"updatedAt"`
}

func Register(e *echo.Echo, app *functions.App, _ *zap.Logger) {
	functions.UpdateTaxSettingsRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return handle(ctx, app, req)
	}))
}

func Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return handle(ctx, app, req)
}

func handle(ctx context.Context, app *functions.App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdministrator(req) {
		return lambdautil.ErrorResponse(req, apperr.Forbidden("Solo el perfil administrador puede modificar la configuración tributaria"))
	}
	var settings program.TaxSettings
	if err := lambdautil.BindJSON(req, &settings); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	if err := lambdautil.ValidateStruct(settings); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	item, err := attributevalue.MarshalMap(taxSettingsItem{PK: "CONFIGURATION", SK: "TAX", TaxSettings: settings, UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	_, err = app.DDB.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(app.Config.ConfigurationsTableName), Item: item})
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return lambdautil.SuccessResponse(http.StatusOK, settings)
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
