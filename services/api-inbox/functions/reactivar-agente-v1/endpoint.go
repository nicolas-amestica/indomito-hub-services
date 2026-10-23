package reactivaragentev1

import (
	"context"

	"github.com/aws/aws-lambda-go/events"
	"github.com/labstack/echo/v4"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions"
)

func Handle(ctx context.Context, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(request, err)
	}
	return functions.ReactivateConversationAgent(ctx, app.Mutation, request)
}

func Register(router *echo.Echo, app *functions.App) {
	functions.ReactivateAgentRoute.Register(router, lambdautil.EchoAdapter(func(ctx context.Context, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return functions.ReactivateConversationAgent(ctx, app.Mutation, request)
	}))
}
