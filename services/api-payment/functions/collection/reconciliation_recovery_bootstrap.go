package collection

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"go.uber.org/zap"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
)

// ReconciliationRecoveryHandler ejecuta el rescate periódico con permisos
// limitados a trabajos y cursor; no carga credenciales Khipu ni escribe saldos.
func ReconciliationRecoveryHandler(ctx context.Context) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	defer func() {
		if resultErr != nil {
			zap.NewExample().Error("Recuperacion periodica de conciliaciones fallida", zap.Error(resultErr), zap.String("Stage", os.Getenv("APP_STAGE")), zap.Int("ProviderRecoveryRequired", 1), zap.Any("_aws", map[string]any{"Timestamp": time.Now().UnixMilli(), "CloudWatchMetrics": []any{map[string]any{"Namespace": "Indomito/Payments", "Dimensions": [][]string{{"Stage"}}, "Metrics": []any{map[string]string{"Name": "ProviderRecoveryRequired", "Unit": "Count"}}}}}))
		}
	}()
	_, err := currentStage()
	if err != nil {
		return err
	}
	table, queueURL := os.Getenv("PAYMENTS_TABLE_NAME"), os.Getenv("PAYMENT_RECONCILIATION_QUEUE_URL")
	if table == "" || queueURL == "" {
		return errors.New("recuperacion de conciliaciones no configurada")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return err
	}
	app := ReconciliationRecoveryApp{Accounts: Service{DB: dynamodb.NewFromConfig(cfg), Table: table}, Queue: sqs.NewFromConfig(cfg), QueueURL: queueURL, Now: time.Now}
	return app.Run(ctx)
}
