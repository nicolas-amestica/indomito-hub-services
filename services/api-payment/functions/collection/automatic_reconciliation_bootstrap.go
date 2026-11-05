package collection

import (
	"context"
	"errors"

	"github.com/aws/aws-lambda-go/events"
	"go.uber.org/zap"
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
	return worker.handleBatch(ctx, event, zap.NewExample()), nil
}

func (a AutomaticReconciliationApp) handleBatch(ctx context.Context, event events.SQSEvent, log *zap.Logger) events.SQSEventResponse {
	response := events.SQSEventResponse{BatchItemFailures: []events.SQSBatchItemFailure{}}
	for _, item := range event.Records {
		message, err := decodeReconciliationMessage(item.Body)
		if err == nil {
			err = a.process(ctx, message)
		}
		if err != nil {
			log.Error("Fallo de conciliacion automatica Khipu", zap.String("messageId", item.MessageId), zap.String("attemptId", message.AttemptID), zap.Error(err))
			response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: item.MessageId})
		}
	}
	return response
}
