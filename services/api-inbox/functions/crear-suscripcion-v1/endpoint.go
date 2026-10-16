package crearsuscripcionv1

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
	return functions.CreateSubscription(ctx, a.Subscription, r)
}

func Register(router *echo.Echo, app *functions.App) {
	functions.CreateSubscriptionRoute.Register(router, lambdautil.EchoAdapter(func(ctx context.Context, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return functions.CreateSubscription(ctx, app.Subscription, request)
	}))
}
