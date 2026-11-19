package endpoint

import (
	"context"
	"github.com/aws/aws-lambda-go/events"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-analytics/functions"
)

// Handle consulta el catálogo de viajes con identidad autorizada por el Gateway.
func Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return functions.AnalyticsHandler(ctx, req)
}

// Register registra la misma operación en el adaptador de desarrollo local.
func Register(e *echo.Echo, log *zap.Logger) {
	functions.ListBalanceRoute.Register(e, lambdautil.EchoAdapter(Handle))
}
