package collection

import (
	"context"
	"errors"

	"github.com/aws/aws-lambda-go/events"
)

// ProviderNotificationWorkerHandler reutiliza la configuración DEV del webhook.
func ProviderNotificationWorkerHandler(ctx context.Context, event events.DynamoDBEvent) (events.DynamoDBEventResponse, error) {
	app, err := getWebhookApp(ctx)
	if err != nil {
		return events.DynamoDBEventResponse{}, err
	}
	worker := ProviderNotificationWorkerApp{Accounts: app.Accounts, Verifier: app.Verifier, Zone: app.Zone, Now: app.Now}
	result := worker.Handle(ctx, event)
	if len(result.BatchItemFailures) == len(event.Records) && len(event.Records) > 0 {
		return result, errors.New("ninguna notificación pudo procesarse")
	}
	return result, nil
}
