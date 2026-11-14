package endpoint

import (
	"context"
	"github.com/aws/aws-lambda-go/events"
	"github.com/labstack/echo/v4"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	inbox "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions"
)

func Handle(ctx context.Context, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	service, err := inbox.GetDerivationAccess(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(request, err)
	}
	return inbox.DerivationAccessRequest(ctx, service, request, false)
}
func Register(router *echo.Echo, _ *inbox.App) {
	inbox.ReadDerivationRoute.Register(router, lambdautil.EchoAdapter(Handle))
}
