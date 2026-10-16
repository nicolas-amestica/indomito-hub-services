package functions

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

type websocketLifecycleFake struct{ connectErr, disconnectErr error }

func (f websocketLifecycleFake) Connect(context.Context, string, string) error { return f.connectErr }
func (f websocketLifecycleFake) Disconnect(context.Context, string) error      { return f.disconnectErr }

func TestConnectWebSocketDoesNotExposeInvalidTicket(t *testing.T) {
	request := events.APIGatewayWebsocketProxyRequest{QueryStringParameters: map[string]string{"ticket": "secret"}, RequestContext: events.APIGatewayWebsocketProxyRequestContext{ConnectionID: "connection"}}
	response, err := ConnectWebSocket(context.Background(), websocketLifecycleFake{connectErr: domain.ErrWebSocketTicketInvalid}, request)
	if err != nil || response.StatusCode != http.StatusUnauthorized || response.Body != "" {
		t.Fatalf("unexpected invalid-ticket response: %#v %v", response, err)
	}
	response, _ = ConnectWebSocket(context.Background(), websocketLifecycleFake{connectErr: errors.New("ddb")}, request)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("unexpected internal response: %#v", response)
	}
}
