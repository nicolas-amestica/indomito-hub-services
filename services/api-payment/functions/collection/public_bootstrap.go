package collection

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

var publicMu sync.Mutex
var publicInstance *PublicApp
var publicLoaded time.Time

func loadPublicSecret(ctx context.Context, reader providers.ParameterReader, path string) (string, error) {
	out, err := reader.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(path), WithDecryption: aws.Bool(true)})
	if err != nil || out == nil || out.Parameter == nil || out.Parameter.Type != types.ParameterTypeSecureString {
		return "", errors.New("configuración de sesión no disponible")
	}
	value := aws.ToString(out.Parameter.Value)
	if len(value) < 32 || strings.HasPrefix(value, "COMPLETAR_") || value == "placeholder-pending-configuration" {
		return "", errors.New("configuración de sesión no válida")
	}
	return value, nil
}

func getPublicApp(ctx context.Context) (*PublicApp, error) {
	publicMu.Lock()
	defer publicMu.Unlock()
	if publicInstance != nil && time.Since(publicLoaded) < 5*time.Minute {
		return publicInstance, nil
	}
	if os.Getenv("APP_STAGE") != "dev" || os.Getenv("PAYMENTS_TABLE_NAME") == "" || os.Getenv("CONFIGURATIONS_TABLE_NAME") == "" {
		return nil, errors.New("configuración pública DEV incompleta")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return nil, err
	}
	reader := ssm.NewFromConfig(cfg)
	lookup, err := loadPublicSecret(ctx, reader, "/indomito/dev/payments/lookup-secret")
	if err != nil {
		return nil, err
	}
	signing, err := loadPublicSecret(ctx, reader, "/indomito/dev/auth/payment-session-secret")
	if err != nil {
		return nil, err
	}
	if lookup == signing {
		return nil, errors.New("la clave de sesión debe ser independiente")
	}
	checkoutEnabled := strings.EqualFold(strings.TrimSpace(os.Getenv("PAYMENTS_CHECKOUT_ENABLED")), "true")
	recaptchaSiteKey := ""
	if checkoutEnabled {
		recaptchaConfig, configErr := providers.LoadRecaptchaConfig(ctx, reader, os.Getenv("APP_STAGE"))
		if configErr != nil {
			return nil, configErr
		}
		recaptchaSiteKey = recaptchaConfig.SiteKey
	}
	publicInstance = &PublicApp{Accounts: Service{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")}, LookupSecret: lookup, SessionSecret: signing, ConfigurationsTable: os.Getenv("CONFIGURATIONS_TABLE_NAME"), CheckoutEnabled: checkoutEnabled, RecaptchaSiteKey: recaptchaSiteKey}
	publicLoaded = time.Now()
	return publicInstance, nil
}
