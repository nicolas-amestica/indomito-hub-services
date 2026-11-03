package collection

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
)

type ssmSIIConfigLoader struct {
	Reader siiParameterAPI
	Stage  string
}

func loadTaxDocumentWorker(ctx context.Context) (TaxDocumentWorkerApp, error) {
	stage, err := currentStage()
	if err != nil || os.Getenv("PAYMENTS_TABLE_NAME") == "" {
		return TaxDocumentWorkerApp{}, errors.New("configuración tributaria incompleta")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return TaxDocumentWorkerApp{}, err
	}
	return TaxDocumentWorkerApp{Accounts: Service{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")}, Config: ssmSIIConfigLoader{Reader: ssm.NewFromConfig(cfg), Stage: stage}, Issuer: directSIIssuer{Client: sii.Client{HTTP: &http.Client{Timeout: 20 * time.Second}, Now: time.Now}}, Now: time.Now}, nil
}

func (l ssmSIIConfigLoader) Load(ctx context.Context, now time.Time) (sii.Credentials, error) {
	raw, err := (siiParameterStore{Client: l.Reader, Stage: l.Stage}).Load(ctx)
	if err != nil {
		return sii.Credentials{}, errors.New("configuración SII no disponible")
	}
	return sii.ParseConfig(raw, now)
}

// TaxDocumentWorkerHandler emite solo solicitudes creadas por pagos Khipu conciliados.
func TaxDocumentWorkerHandler(ctx context.Context, event events.DynamoDBEvent) (events.DynamoDBEventResponse, error) {
	worker, err := loadTaxDocumentWorker(ctx)
	if err != nil {
		return events.DynamoDBEventResponse{}, err
	}
	result := worker.Handle(ctx, event)
	if len(result.BatchItemFailures) == len(event.Records) && len(event.Records) > 0 {
		return result, errors.New("ninguna boleta pudo emitirse")
	}
	return result, nil
}
