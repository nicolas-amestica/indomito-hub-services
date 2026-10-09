package collection

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
)

var receiptVerificationMu sync.Mutex
var receiptVerificationInstance *PublicApp
var receiptVerificationLoaded time.Time

// getReceiptVerificationApp carga solo las dependencias necesarias para
// validar un comprobante público. No debe depender de checkout, reCAPTCHA,
// sesiones de pasajeros ni configuraciones administrativas.
func getReceiptVerificationApp(ctx context.Context) (*PublicApp, error) {
	receiptVerificationMu.Lock()
	defer receiptVerificationMu.Unlock()
	if receiptVerificationInstance != nil && time.Since(receiptVerificationLoaded) < 5*time.Minute {
		return receiptVerificationInstance, nil
	}
	if os.Getenv("APP_STAGE") != "dev" || os.Getenv("PAYMENTS_TABLE_NAME") == "" {
		return nil, errors.New("configuración de verificación DEV incompleta")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return nil, err
	}
	lookup, err := loadPublicSecret(ctx, ssm.NewFromConfig(cfg), "/indomito/dev/payments/lookup-secret")
	if err != nil {
		return nil, err
	}
	receiptVerificationInstance = &PublicApp{
		Accounts:     Service{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")},
		LookupSecret: lookup,
	}
	receiptVerificationLoaded = time.Now()
	return receiptVerificationInstance, nil
}
