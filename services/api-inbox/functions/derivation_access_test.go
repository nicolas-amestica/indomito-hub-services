package functions

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

type derivationAccessFake struct {
	calls     int
	err       error
	sessionID string
}

func (s *derivationAccessFake) Redeem(context.Context, string) (domain.DerivationAccessSession, error) {
	s.calls++
	return domain.DerivationAccessSession{}, s.err
}
func (s *derivationAccessFake) Read(_ context.Context, sessionID string, _ string) (domain.DerivationAccessPage, error) {
	s.calls++
	s.sessionID = sessionID
	return domain.DerivationAccessPage{}, s.err
}
func TestDerivationAccessHTTPRejectsAdditionalScopeAndMalformedJSON(t *testing.T) {
	for _, body := range []string{`{"token":"x","phone":"56999999999"}`, `{"token":"x"} {"token":"y"}`, `{"token":"x","sessionToken":"y"}`, strings.Repeat("a", 2049)} {
		service := &derivationAccessFake{}
		response, err := DerivationAccessRequest(context.Background(), service, events.APIGatewayV2HTTPRequest{Body: body}, true)
		if err != nil || response.StatusCode != 400 || service.calls != 0 {
			t.Fatalf("invalid request accepted status=%d calls=%d err=%v", response.StatusCode, service.calls, err)
		}
	}
}
func TestDerivationAccessHTTPNeverCachesOrExposesPermissionErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, 200}, {domain.ErrDerivationAccessInvalid, 410}, {errors.New("private token details"), 500}} {
		service := &derivationAccessFake{err: tc.err}
		response, err := DerivationAccessRequest(context.Background(), service, authorizedDerivationRequest(`{"before":""}`), false)
		if err != nil || response.StatusCode != tc.status || !strings.Contains(response.Headers["cache-control"], "no-store") || strings.Contains(response.Body, "private token details") {
			t.Fatalf("unsafe response %#v err=%v", response, err)
		}
	}
}

func authorizedDerivationRequest(body string) events.APIGatewayV2HTTPRequest {
	id := strings.Repeat("a", 64)
	return events.APIGatewayV2HTTPRequest{Body: body, RequestContext: events.APIGatewayV2HTTPRequestContext{Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]any{"userId": "derivation:" + id, "derivationScope": "conversation:read", "derivationSessionId": id}}}}
}

func TestDerivationReadRequiresTrustedAuthorizerContext(t *testing.T) {
	for _, request := range []events.APIGatewayV2HTTPRequest{
		{Body: `{"sessionToken":"attacker-controlled"}`, Headers: map[string]string{"authorization": "Bearer attacker-controlled"}},
		{Body: `{"before":""}`, RequestContext: events.APIGatewayV2HTTPRequestContext{Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]any{"userId": "normal-admin"}}}},
	} {
		service := &derivationAccessFake{}
		response, err := DerivationAccessRequest(context.Background(), service, request, false)
		if err != nil || response.StatusCode != 401 || service.calls != 0 {
			t.Fatalf("read bypassed authorizer: %#v %v", response, err)
		}
	}
	service := &derivationAccessFake{}
	response, err := DerivationAccessRequest(context.Background(), service, authorizedDerivationRequest(`{"before":""}`), false)
	if err != nil || response.StatusCode != 200 || service.sessionID != strings.Repeat("a", 64) {
		t.Fatalf("invalid authorized read: %#v %v", response, err)
	}
	for _, body := range []string{`{"sessionToken":"forged"}`, `{"token":"forged"}`, `{"phone":"56999999999"}`} {
		response, err = DerivationAccessRequest(context.Background(), &derivationAccessFake{}, authorizedDerivationRequest(body), false)
		if err != nil || response.StatusCode != 400 {
			t.Fatalf("client expanded scope: %#v %v", response, err)
		}
	}
}

func TestDerivationReadRejectsAlteredAuthorizerScope(t *testing.T) {
	for _, field := range []string{"derivationScope", "derivationSessionId", "userId"} {
		request := authorizedDerivationRequest(`{"before":""}`)
		request.RequestContext.Authorizer.Lambda[field] = "forged"
		service := &derivationAccessFake{}
		response, err := DerivationAccessRequest(context.Background(), service, request, false)
		if err != nil || response.StatusCode != 401 || service.calls != 0 {
			t.Fatalf("altered %s accepted", field)
		}
	}
}

func TestDerivationRoutesSeparatePublicExchangeFromProtectedRead(t *testing.T) {
	raw, err := os.ReadFile("../serverless.ts")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, tc := range []struct{ name, access string }{{"fn-canjear-derivacion-v1", "public: true"}, {"fn-leer-derivacion-v1", "public: false"}} {
		start := strings.Index(source, `name: "`+tc.name+`"`)
		if start < 0 {
			t.Fatal("missing route", tc.name)
		}
		end := strings.Index(source[start:], "policies:")
		if end < 0 || !strings.Contains(source[start:start+end], tc.access) {
			t.Fatalf("wrong authorizer boundary for %s", tc.name)
		}
	}
}
