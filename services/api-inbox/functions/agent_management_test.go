package functions

import (
	"github.com/aws/aws-lambda-go/events"
	"testing"
)

func agentManagerRequest(profile string) events.APIGatewayV2HTTPRequest {
	return events.APIGatewayV2HTTPRequest{RequestContext: events.APIGatewayV2HTTPRequestContext{Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]any{"userId": "user-1", "profileCode": profile}}}}
}

func TestRequireAgentManagerAllowsOnlyADM(t *testing.T) {
	if err := requireAgentManager(agentManagerRequest("ADM")); err != nil {
		t.Fatalf("ADM rejected: %v", err)
	}
	if err := requireAgentManager(agentManagerRequest("VEN")); err == nil || err.HTTPStatus() != 403 {
		t.Fatalf("expected forbidden, got %#v", err)
	}
	if err := requireAgentManager(events.APIGatewayV2HTTPRequest{}); err == nil || err.HTTPStatus() != 401 {
		t.Fatalf("expected unauthorized, got %#v", err)
	}
}
