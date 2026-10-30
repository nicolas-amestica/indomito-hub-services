package collection

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
)

type secretsManagerSIIConfigLoader struct {
	Reader *secretsmanager.Client
	Stage  string
}

func (l secretsManagerSIIConfigLoader) Load(ctx context.Context, now time.Time) (sii.Credentials, error) {
	name := "indomito/" + l.Stage + "/payments/sii/boleta-config"
	result, err := l.Reader.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(name)})
	if err != nil || result == nil || result.SecretString == nil {
		return sii.Credentials{}, errors.New("configuración SII no disponible")
	}
	return sii.ParseConfig([]byte(aws.ToString(result.SecretString)), now)
}

// TaxDocumentWorkerHandler emite solo solicitudes creadas por pagos Khipu conciliados.
func TaxDocumentWorkerHandler(ctx context.Context, event events.DynamoDBEvent) (events.DynamoDBEventResponse, error) {
	stage, err := currentStage()
	if err != nil || os.Getenv("PAYMENTS_TABLE_NAME") == "" {
		return events.DynamoDBEventResponse{}, errors.New("configuración tributaria incompleta")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return events.DynamoDBEventResponse{}, err
	}
	worker := TaxDocumentWorkerApp{
		Accounts: Service{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")},
		Config:   secretsManagerSIIConfigLoader{Reader: secretsmanager.NewFromConfig(cfg), Stage: stage},
		Issuer:   directSIIssuer{Client: sii.Client{HTTP: &http.Client{Timeout: 20 * time.Second}, Now: time.Now}},
		Now:      time.Now,
	}
	result := worker.Handle(ctx, event)
	if len(result.BatchItemFailures) == len(event.Records) && len(event.Records) > 0 {
		return result, errors.New("ninguna boleta pudo emitirse")
	}
	return result, nil
}
