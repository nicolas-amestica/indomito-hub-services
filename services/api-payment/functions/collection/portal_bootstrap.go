package collection

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
)

// PortalCheckoutHandler exige el contexto de pasajero antes de resolver secretos del proveedor.
func PortalCheckoutHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.RequestContext.Authorizer == nil || req.RequestContext.Authorizer.Lambda["paymentAccess"] != "passenger" {
		return portalFailure(403), nil
	}
	app, err := getWebhookApp(ctx)
	if err != nil {
		return portalFailure(503), nil
	}
	gateway, ok := app.Verifier.(CheckoutGateway)
	if !ok {
		return portalFailure(503), nil
	}
	base := strings.TrimRight(os.Getenv("PAYMENTS_BASE_URL"), "/")
	portalBase := strings.TrimRight(os.Getenv("PAYMENTS_PORTAL_URL"), "/")
	if !secureCheckoutURL(base) || !secureCheckoutURL(portalBase) {
		return portalFailure(503), nil
	}
	recaptcha, err := getRecaptcha(ctx)
	if err != nil {
		return portalFailure(503), nil
	}
	portal := PortalApp{Accounts: app.Accounts, Gateway: gateway, Recaptcha: recaptcha, Now: time.Now, URLs: CheckoutURLs{Return: portalBase + "/retorno", Cancel: portalBase + "/cancelado", Notify: base + "/pagos/cuotas/khipu/notificaciones"}}
	return portal.HandleCheckout(ctx, req), nil
}

// PortalAttemptHandler no necesita credenciales Khipu ni permisos de escritura.
func PortalAttemptHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.RequestContext.Authorizer == nil || req.RequestContext.Authorizer.Lambda["paymentAccess"] != "passenger" {
		return portalFailure(403), nil
	}
	if os.Getenv("APP_STAGE") != "dev" || os.Getenv("PAYMENTS_TABLE_NAME") == "" {
		return portalFailure(503), nil
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return portalFailure(503), nil
	}
	portal := PortalApp{Accounts: Service{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")}, Now: time.Now, CheckoutEnabled: strings.EqualFold(strings.TrimSpace(os.Getenv("PAYMENTS_CHECKOUT_ENABLED")), "true")}
	return portal.HandleAttempt(ctx, req), nil
}

// PortalAccountHandler refresca cuotas después del retorno sin repetir RUT ni código.
func PortalAccountHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.RequestContext.Authorizer == nil || req.RequestContext.Authorizer.Lambda["paymentAccess"] != "passenger" {
		return portalFailure(403), nil
	}
	if os.Getenv("APP_STAGE") != "dev" || os.Getenv("PAYMENTS_TABLE_NAME") == "" {
		return portalFailure(503), nil
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return portalFailure(503), nil
	}
	portal := PortalApp{Accounts: Service{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")}, Now: time.Now, CheckoutEnabled: strings.EqualFold(strings.TrimSpace(os.Getenv("PAYMENTS_CHECKOUT_ENABLED")), "true")}
	return portal.HandleAccount(ctx, req), nil
}
