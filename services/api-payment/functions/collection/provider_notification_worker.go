package collection

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

// ProviderNotificationWorkerApp procesa únicamente jobs creados por la bandeja
// autenticada. Reporta fallos parciales para que el stream reintente el registro.
type ProviderNotificationWorkerApp struct {
	Accounts Service
	Verifier PaymentVerifier
	Zone     *time.Location
	Now      func() time.Time
}

// Handle procesa un lote del stream. Los registros ajenos se ignoran: el filtro
// de infraestructura es defensa de costo, esta validación es la frontera lógica.
func (a ProviderNotificationWorkerApp) Handle(ctx context.Context, event events.DynamoDBEvent) events.DynamoDBEventResponse {
	response := events.DynamoDBEventResponse{BatchItemFailures: []events.DynamoDBBatchItemFailure{}}
	for _, change := range event.Records {
		pk, pkOK := streamString(change.Change.NewImage, "pk")
		sk, skOK := streamString(change.Change.NewImage, "sk")
		status, statusOK := streamString(change.Change.NewImage, "status")
		paymentID := strings.TrimPrefix(sk, "PENDING#")
		if !pkOK || !skOK || !statusOK || !strings.HasPrefix(pk, "PROVIDER_JOB#") || status != "PENDING" || sk == paymentID || !checkoutPaymentID.MatchString(paymentID) || a.Verifier == nil || a.Zone == nil || a.Now == nil {
			continue
		}
		if _, err := a.Accounts.ProcessProviderNotification(ctx, a.Verifier, paymentID, a.Now(), a.Zone); err != nil {
			response.BatchItemFailures = append(response.BatchItemFailures, events.DynamoDBBatchItemFailure{ItemIdentifier: change.EventID})
		}
	}
	return response
}

func streamString(image map[string]events.DynamoDBAttributeValue, name string) (string, bool) {
	value, ok := image[name]
	if !ok || value.DataType() != events.DataTypeString || value.String() == "" {
		return "", false
	}
	return value.String(), true
}
