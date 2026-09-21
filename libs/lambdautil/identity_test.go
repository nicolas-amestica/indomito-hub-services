package lambdautil

import (
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

// requestWithAuthorizerContext arma una solicitud con el contexto del
// authorizer indicado. Un contexto nil representa la solicitud que llega
// cuando el authorizer no participo.
func requestWithAuthorizerContext(lambdaContext map[string]any) events.APIGatewayV2HTTPRequest {
	req := events.APIGatewayV2HTTPRequest{}
	if lambdaContext != nil {
		req.RequestContext.Authorizer = &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
			Lambda: lambdaContext,
		}
	}
	return req
}

// TestUserIDFromContext_ReadsAuthorizerContext verifica que la identidad se lea
// de requestContext.authorizer.lambda, tanto con la clave del authorizer propio
// como con el sujeto de un token con claims estandar.
func TestUserIDFromContext_ReadsAuthorizerContext(t *testing.T) {
	tests := []struct {
		name   string
		req    events.APIGatewayV2HTTPRequest
		wantID string
	}{
		{
			name:   "clave userId",
			req:    requestWithAuthorizerContext(map[string]any{AuthorizerUserIDKey: "01JQZ8USUARIO"}),
			wantID: "01JQZ8USUARIO",
		},
		{
			name:   "clave sub como alternativa",
			req:    requestWithAuthorizerContext(map[string]any{"sub": "01JQZ8SUJETO"}),
			wantID: "01JQZ8SUJETO",
		},
		{
			name: "userId tiene prioridad sobre sub",
			req: requestWithAuthorizerContext(map[string]any{
				AuthorizerUserIDKey: "01JQZ8USUARIO",
				"sub":               "01JQZ8SUJETO",
			}),
			wantID: "01JQZ8USUARIO",
		},
		{
			name:   "identidad con espacios alrededor",
			req:    requestWithAuthorizerContext(map[string]any{AuthorizerUserIDKey: "  01JQZ8USUARIO  "}),
			wantID: "01JQZ8USUARIO",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, err := UserIDFromContext(tt.req)
			if err != nil {
				t.Fatalf("UserIDFromContext devolvio error: %v", err)
			}
			if userID != tt.wantID {
				t.Errorf("userID = %q, want %q", userID, tt.wantID)
			}
		})
	}
}

// TestUserIDFromContext_FailsWithoutIdentity verifica que un endpoint protegido
// sin identidad en el contexto no continue: la ausencia produce UNAUTHORIZED,
// nunca un identificador vacio.
func TestUserIDFromContext_FailsWithoutIdentity(t *testing.T) {
	tests := []struct {
		name string
		req  events.APIGatewayV2HTTPRequest
	}{
		{
			name: "sin contexto de authorizer",
			req:  requestWithAuthorizerContext(nil),
		},
		{
			name: "contexto de authorizer vacio",
			req:  requestWithAuthorizerContext(map[string]any{}),
		},
		{
			name: "contexto sin las claves de identidad",
			req:  requestWithAuthorizerContext(map[string]any{"allowances": "crud"}),
		},
		{
			name: "identidad en blanco",
			req:  requestWithAuthorizerContext(map[string]any{AuthorizerUserIDKey: "   "}),
		},
		{
			name: "identidad que no es texto",
			req:  requestWithAuthorizerContext(map[string]any{AuthorizerUserIDKey: 12345}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, err := UserIDFromContext(tt.req)
			if err == nil {
				t.Fatalf("UserIDFromContext acepto una solicitud sin identidad, devolvio %q", userID)
			}
			if userID != "" {
				t.Errorf("userID = %q, want cadena vacia", userID)
			}
			if code := apperr.From(err).Code(); code != apperr.CodeUnauthorized {
				t.Errorf("code = %q, want %q", code, apperr.CodeUnauthorized)
			}
		})
	}
}

// TestUserIDFromContext_IgnoresBodyIdentity verifica el Requirement 19.8: un
// userId enviado en el cuerpo no identifica a nadie.
func TestUserIDFromContext_IgnoresBodyIdentity(t *testing.T) {
	req := requestWithAuthorizerContext(nil)
	req.Body = `{"userId":"01JQZ8IMPOSTOR"}`
	req.Headers = map[string]string{"x-user-id": "01JQZ8IMPOSTOR"}

	if _, err := UserIDFromContext(req); err == nil {
		t.Fatal("UserIDFromContext acepto una identidad enviada por el cliente")
	}
}
