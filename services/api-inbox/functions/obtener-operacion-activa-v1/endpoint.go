package obteneroperacionactivav1

import (
	"context"
	"github.com/aws/aws-lambda-go/events"
	"github.com/labstack/echo/v4"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions"
)

func Handle(ctx context.Context, r events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	a, e := functions.GetApp(ctx)
	if e != nil {
		return lambdautil.ErrorResponse(r, e)
	}
	return functions.GetActiveBulkOperation(ctx, a.Bulk, r)
}

func Register(router *echo.Echo, app *functions.App) {
	functions.GetActiveBulkRoute.Register(router, lambdautil.EchoAdapter(func(ctx context.Context, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return functions.GetActiveBulkOperation(ctx, app.Bulk, request)
	}))
}
