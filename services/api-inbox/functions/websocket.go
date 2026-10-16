package functions

import (
	"context"
	"errors"
	"net/http"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

// WebSocketLifecycle exposes the connection lifecycle used by API Gateway routes.
type WebSocketLifecycle interface {
	Connect(context.Context, string, string) error
	Disconnect(context.Context, string) error
}

// ConnectWebSocket consumes the query-string ticket without logging it.
func ConnectWebSocket(ctx context.Context, service WebSocketLifecycle, request events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
	err := service.Connect(ctx, request.RequestContext.ConnectionID, request.QueryStringParameters["ticket"])
	if errors.Is(err, domain.ErrWebSocketTicketInvalid) {
		return events.APIGatewayProxyResponse{StatusCode: http.StatusUnauthorized}, nil
	}
	if err != nil {
		return events.APIGatewayProxyResponse{StatusCode: http.StatusInternalServerError}, nil
	}
	return events.APIGatewayProxyResponse{StatusCode: http.StatusOK}, nil
}

// DisconnectWebSocket removes connection state idempotently.
func DisconnectWebSocket(ctx context.Context, service WebSocketLifecycle, request events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
	if err := service.Disconnect(ctx, request.RequestContext.ConnectionID); err != nil {
		return events.APIGatewayProxyResponse{StatusCode: http.StatusInternalServerError}, nil
	}
	return events.APIGatewayProxyResponse{StatusCode: http.StatusOK}, nil
}
