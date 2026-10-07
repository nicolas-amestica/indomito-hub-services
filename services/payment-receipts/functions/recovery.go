package functions

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// RecoveryResult describe una recuperación manual paginada.
type RecoveryResult struct {
	Apply      bool           `json:"apply"`
	Results    []RecoveryItem `json:"results"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

// RecoveryItem nunca contiene correo ni datos financieros.
type RecoveryItem struct {
	ReceiptID  string `json:"receiptId"`
	DeliveryID string `json:"deliveryId,omitempty"`
	Status     string `json:"status"`
}

// RecoverJobs simula por defecto; apply puede reenviar correos y exige toda la configuración DEV.
func RecoverJobs(ctx context.Context, date string, limit int32, cursor string, apply bool) (RecoveryResult, error) {
	parsed, err := time.Parse(time.DateOnly, date)
	if err != nil || parsed.Format(time.DateOnly) != date || limit < 1 || limit > 100 || (cursor != "" && !receiptIDPattern.MatchString(cursor)) || os.Getenv("APP_STAGE") != "dev" || os.Getenv("PAYMENTS_TABLE_NAME") == "" {
		return RecoveryResult{}, errors.New("INVALID_RECOVERY_REQUEST")
	}
	awsConfig, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return RecoveryResult{}, err
	}
	root := store{client: dynamodb.NewFromConfig(awsConfig), table: os.Getenv("PAYMENTS_TABLE_NAME"), now: time.Now}
	rows, next, err := root.jobs(ctx, date, limit, cursor)
	if err != nil {
		return RecoveryResult{}, err
	}
	result := RecoveryResult{Apply: apply, Results: []RecoveryItem{}, NextCursor: next}
	var deps dependencies
	if apply {
		deps, err = loadDependencies(ctx)
		if err != nil {
			return RecoveryResult{}, err
		}
	}
	for _, row := range rows {
		id := row.ReceiptID
		if row.DeliveryID != "" {
			id = row.DeliveryID
		}
		if row.Status != "PENDING" {
			continue
		}
		if row.PK != "JOB#"+date || row.SK != "PENDING#"+id || !receiptIDPattern.MatchString(row.ReceiptID) || (row.DeliveryID != "" && !receiptIDPattern.MatchString(row.DeliveryID)) {
			return RecoveryResult{}, errors.New("INVALID_RECOVERY_JOB")
		}
		item := RecoveryItem{ReceiptID: row.ReceiptID, DeliveryID: row.DeliveryID, Status: "WOULD_RETRY"}
		if apply {
			status, processErr := processJob(ctx, job{PK: row.PK, SK: row.SK, ReceiptID: row.ReceiptID, DeliveryID: row.DeliveryID}, deps)
			if processErr != nil {
				item.Status = "RETRY_FAILED"
			} else {
				item.Status = status
			}
		}
		result.Results = append(result.Results, item)
	}
	return result, nil
}
