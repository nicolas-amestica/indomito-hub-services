package obtenerconversacionv1

import (
	"context"

	"github.com/aws/aws-lambda-go/events"
	"github.com/labstack/echo/v4"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	inboxfunctions "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions"
)

func Handle(ctx context.Context, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := inboxfunctions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(request, err)
	}
	return inboxfunctions.GetConversation(ctx, app.Detail, request)
}

func Register(router *echo.Echo, app *inboxfunctions.App) {
	inboxfunctions.GetConversationRoute.Register(router, lambdautil.EchoAdapter(func(ctx context.Context, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return inboxfunctions.GetConversation(ctx, app.Detail, request)
	}))
}
