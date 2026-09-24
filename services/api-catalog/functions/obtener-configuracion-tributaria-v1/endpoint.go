package gettaxsettings

import (
	"context"
	"net/http"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions"
)

func Register(e *echo.Echo, app *functions.App, _ *zap.Logger) {
	functions.GetTaxSettingsRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
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
	out, err := app.DDB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(app.Config.ConfigurationsTableName), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "CONFIGURATION"}, "sk": &types.AttributeValueMemberS{Value: "TAX"}}, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	if len(out.Item) == 0 {
		return lambdautil.ErrorResponse(req, apperr.NotFound("No existe configuración tributaria"))
	}
	var settings program.TaxSettings
	if err := attributevalue.UnmarshalMap(out.Item, &settings); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return lambdautil.SuccessResponse(http.StatusOK, settings)
}
