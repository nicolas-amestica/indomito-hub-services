package collection

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
	_ "time/tzdata" // Incluye reglas de America/Santiago en el binario Lambda.

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

var webhookMu sync.Mutex
var webhookInstance *WebhookApp
var webhookLoaded time.Time

func getWebhookApp(ctx context.Context) (*WebhookApp, error) {
	webhookMu.Lock()
	defer webhookMu.Unlock()
	if webhookInstance != nil && time.Since(webhookLoaded) < 5*time.Minute {
		return webhookInstance, nil
	}
	if os.Getenv("APP_STAGE") != "dev" || os.Getenv("PAYMENTS_TABLE_NAME") == "" {
		return nil, errors.New("configuración DEV incompleta")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return nil, err
	}
	secrets, err := providers.LoadKhipuDevelopment(ctx, ssm.NewFromConfig(cfg), "dev")
	if err != nil {
		return nil, err
	}
	zone, err := time.LoadLocation("America/Santiago")
	if err != nil {
		return nil, err
	}
	webhookInstance = &WebhookApp{Accounts: Service{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")}, Verifier: secrets.Client, Secret: secrets.WebhookSecret, Zone: zone, Now: time.Now}
	webhookLoaded = time.Now()
	return webhookInstance, nil
}

// InstallmentWebhookHandler es la entrada DEV; no expone secretos ni errores internos.
func InstallmentWebhookHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.RequestContext.HTTP.Method != "POST" {
		return webhookResponse(405), nil
	}
	if len(req.Body) > 90*1024 {
		return webhookResponse(413), nil
	}
	a, err := getWebhookApp(ctx)
	if err != nil {
		return webhookResponse(503), nil
	}
	return a.Handle(ctx, req), nil
}
