// Package cloud implementa exclusivamente endpoints seguros de integración DEV.
// No importa el servidor local, sus sesiones ni su simulador.
package cloud

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

// Database limita la persistencia a claves conocidas y escrituras condicionales.
type Database interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

// Gateway permite probar el flujo sin red ni dinero.
type Gateway interface {
	Create(context.Context, providers.CheckoutRequest) (providers.Checkout, error)
	Verify(context.Context, string, string, int64) (providers.VerifiedPayment, error)
	DevelopmentBank(context.Context) (string, error)
}

// App agrupa dependencias; los secretos nunca se serializan ni se registran.
type App struct {
	DB            Database
	Gateway       Gateway
	Table         string
	BaseURL       string
	WebhookSecret string
	Now           func() time.Time
}

var appMu sync.Mutex
var instance *App
var loadedAt time.Time

// GetApp reintenta fallos de arranque y refresca secretos cada cinco minutos.
func GetApp(ctx context.Context) (*App, error) {
	appMu.Lock()
	defer appMu.Unlock()
	if instance != nil && time.Since(loadedAt) < 5*time.Minute {
		return instance, nil
	}
	if os.Getenv("APP_STAGE") != "dev" || os.Getenv("PAYMENTS_TABLE_NAME") == "" || os.Getenv("PAYMENTS_BASE_URL") == "" {
		return nil, errors.New("configuración DEV incompleta")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return nil, errors.New("configuración AWS no disponible")
	}
	secrets, err := providers.LoadKhipuDevelopment(ctx, ssm.NewFromConfig(cfg), "dev")
	if err != nil {
		return nil, err
	}
	instance = &App{DB: dynamodb.NewFromConfig(cfg), Gateway: secrets.Client, Table: os.Getenv("PAYMENTS_TABLE_NAME"), BaseURL: os.Getenv("PAYMENTS_BASE_URL"), WebhookSecret: secrets.WebhookSecret, Now: time.Now}
	loadedAt = time.Now()
	return instance, nil
}
