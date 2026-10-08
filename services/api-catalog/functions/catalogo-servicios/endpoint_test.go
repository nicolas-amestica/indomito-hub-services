package servicecatalog

import (
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestValidatedScope(t *testing.T) {
	t.Parallel()
	if got, err := validatedScope(" ctz "); err != nil || got != "CTZ" {
		t.Fatalf("validatedScope() = %q, %v", got, err)
	}
	if _, err := validatedScope("PGR"); err == nil {
		t.Fatal("validatedScope(PGR) debio rechazar el scope")
	}
}

func TestServiceFromInputUsesDirectAccessPattern(t *testing.T) {
	t.Parallel()
	item := serviceFromInput("CAT#SRV#catalog", "CTZ", "01M4C8EFRFHRK7DJW09DT6J6CX", serviceInput{
		Glosa: "Hotel", Currency: "USD", ChargeType: "per_passenger_day", Active: true, Default: true,
	})
	if item.PK != "CAT#SRV#catalog" || item.SK != "FRM#SCP#CTZ#01M4C8EFRFHRK7DJW09DT6J6CX" {
		t.Fatalf("patron de acceso inesperado: pk=%q sk=%q", item.PK, item.SK)
	}
}

func TestIsAdministrator(t *testing.T) {
	t.Parallel()
	req := events.APIGatewayV2HTTPRequest{RequestContext: events.APIGatewayV2HTTPRequestContext{
		Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]interface{}{"profileCode": "ADMIN"}},
	}}
	if !isAdministrator(req) {
		t.Fatal("ADMIN debio poder administrar el catalogo")
	}
	req.RequestContext.Authorizer.Lambda["profileCode"] = "GESTOR"
	if isAdministrator(req) {
		t.Fatal("GESTOR no debe administrar el catalogo")
	}
}
