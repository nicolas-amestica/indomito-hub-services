package lambdautil

import (
	"encoding/base64"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

type bindTarget struct {
	Name  string `json:"name"`
	Total int    `json:"total"`
}

// TestBindJSON_DecodesBody verifica que BindJSON resuelva tanto el cuerpo en
// texto plano como el codificado en base64, que es el que API Gateway entrega
// cuando isBase64Encoded viene en true.
func TestBindJSON_DecodesBody(t *testing.T) {
	const body = `{"name":"gira","total":42}`

	tests := []struct {
		name string
		req  events.APIGatewayV2HTTPRequest
	}{
		{
			name: "cuerpo en texto plano",
			req:  events.APIGatewayV2HTTPRequest{Body: body},
		},
		{
			name: "cuerpo en base64",
			req: events.APIGatewayV2HTTPRequest{
				Body:            base64.StdEncoding.EncodeToString([]byte(body)),
				IsBase64Encoded: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target bindTarget
			if err := BindJSON(tt.req, &target); err != nil {
				t.Fatalf("BindJSON devolvio error: %v", err)
			}
			if target.Name != "gira" {
				t.Errorf("name = %q, want gira", target.Name)
			}
			if target.Total != 42 {
				t.Errorf("total = %d, want 42", target.Total)
			}
		})
	}
}

// TestBindJSON_RejectsUnusableBody verifica que un cuerpo ausente, en blanco,
// mal formado o mal codificado produzca INVALID_REQUEST_FORMAT: en los cuatro
// casos la solicitud no alcanza a validarse.
func TestBindJSON_RejectsUnusableBody(t *testing.T) {
	tests := []struct {
		name string
		req  events.APIGatewayV2HTTPRequest
	}{
		{
			name: "cuerpo ausente",
			req:  events.APIGatewayV2HTTPRequest{},
		},
		{
			name: "cuerpo en blanco",
			req:  events.APIGatewayV2HTTPRequest{Body: "   \n"},
		},
		{
			name: "json mal formado",
			req:  events.APIGatewayV2HTTPRequest{Body: `{"name":`},
		},
		{
			name: "base64 invalido",
			req:  events.APIGatewayV2HTTPRequest{Body: "no-es-base64!!", IsBase64Encoded: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target bindTarget

			err := BindJSON(tt.req, &target)
			if err == nil {
				t.Fatal("BindJSON acepto un cuerpo inutilizable")
			}
			if code := apperr.From(err).Code(); code != apperr.CodeInvalidRequestFormat {
				t.Errorf("code = %q, want %q", code, apperr.CodeInvalidRequestFormat)
			}
		})
	}
}
