package collection

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

var recaptchaMu sync.Mutex
var recaptchaInstance *providers.Recaptcha
var recaptchaLoaded time.Time

func getRecaptcha(ctx context.Context) (*providers.Recaptcha, error) {
	recaptchaMu.Lock()
	defer recaptchaMu.Unlock()
	if recaptchaInstance != nil && time.Since(recaptchaLoaded) < 5*time.Minute {
		return recaptchaInstance, nil
	}
	stage := os.Getenv("APP_STAGE")
	if stage != "dev" && stage != "prd" {
		return nil, errors.New("ambiente reCAPTCHA inválido")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return nil, err
	}
	config, err := providers.LoadRecaptchaConfig(ctx, ssm.NewFromConfig(cfg), stage)
	if err != nil {
		return nil, err
	}
	recaptchaInstance, err = providers.NewRecaptcha(config)
	if err != nil {
		return nil, err
	}
	recaptchaLoaded = time.Now()
	return recaptchaInstance, nil
}
