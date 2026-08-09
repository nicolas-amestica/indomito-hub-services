package helloworldv1

import (
	"context"
	"net/http"

	"github.com/aws/aws-lambda-go/events"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/services/api-auth/functions"
)

// Register registers the local Echo route for this endpoint.
func Register(e *echo.Echo, _ *zap.Logger) {
	e.POST("/v1/autenticacion/hello", echoHandler)
}

// echoHandler adapts the Lambda handler into an Echo handler for local dev.
func echoHandler(c echo.Context) error {
	resp, _ := Handle(c.Request().Context(), events.APIGatewayV2HTTPRequest{})
	return c.JSONBlob(resp.StatusCode, []byte(resp.Body))
}

// Handle processes the Lambda/API Gateway invocation.
func Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, err := functions.GetApp(ctx); err != nil {
		return events.APIGatewayV2HTTPResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       `{"error":"internal server error"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	_ = req // no se usa input en este endpoint de ejemplo

	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusOK,
		Body:       `{"message":"Hello World"}`,
		Headers:    map[string]string{"Content-Type": "application/json"},
	}, nil
}
