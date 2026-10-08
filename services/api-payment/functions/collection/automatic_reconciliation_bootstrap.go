package collection

import (
	"context"
	"errors"

	"github.com/aws/aws-lambda-go/events"
)

// AutomaticReconciliationHandler procesa mensajes SQS con fallos parciales.
func AutomaticReconciliationHandler(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	app, err := getWebhookApp(ctx)
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	inspector, ok := app.Verifier.(PaymentInspector)
	if !ok {
		return events.SQSEventResponse{}, errors.New("verificador Khipu no permite inspeccion")
	}
	worker := AutomaticReconciliationApp{Accounts: app.Accounts, Inspector: inspector, Verifier: app.Verifier, Zone: app.Zone, Now: app.Now}
	response := events.SQSEventResponse{BatchItemFailures: []events.SQSBatchItemFailure{}}
	for _, item := range event.Records {
		message, decodeErr := decodeReconciliationMessage(item.Body)
		if decodeErr != nil || worker.process(ctx, message) != nil {
			response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: item.MessageId})
		}
	}
	return response, nil
}
