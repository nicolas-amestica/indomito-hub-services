package collection

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/dteinbox"
)

type receivedSyncInvoker interface {
	Invoke(context.Context, *lambda.InvokeInput, ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

type receivedSyncStore interface {
	State(context.Context) (dteinbox.SyncState, error)
	BeginSyncRequest(context.Context, string, string, time.Time) (*dteinbox.SyncRequest, bool, error)
	TransitionSyncRequest(context.Context, dteinbox.SyncRequest, string, string, time.Time) (*dteinbox.SyncRequest, error)
}

func requestReceivedSync(ctx context.Context, req events.APIGatewayV2HTTPRequest, store receivedSyncStore, invoker receivedSyncInvoker, stage string, now time.Time) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	if req.RequestContext.HTTP.Method != http.MethodPost {
		return adminFailure(req, 405, "METHOD_NOT_ALLOWED")
	}
	if stage != "dev" && stage != "prd" {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	state, err := store.State(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	job, claimed, err := store.BeginSyncRequest(ctx, actor, ulid.Make().String(), now)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	if claimed {
		payload, marshalErr := json.Marshal(ReceivedSyncEvent{RequestID: job.ID})
		if marshalErr != nil {
			return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		out, invokeErr := invoker.Invoke(ctx, &lambda.InvokeInput{FunctionName: aws.String("api-payment-" + stage + "-fn-sincronizar-dte-recibidos-v1"), InvocationType: types.InvocationTypeEvent, Payload: payload})
		if invokeErr != nil || out == nil || out.StatusCode != 202 {
			if _, err = store.TransitionSyncRequest(ctx, *job, "FAILED", "No fue posible poner la lectura en cola. Puedes volver a solicitarla después de un minuto.", now); err != nil {
				return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
			}
			return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
		}
	}
	return lambdautil.SuccessResponseWithHeaders(202, map[string]any{"queued": claimed, "lastSuccess": state.LastSuccess, "request": job}, map[string]string{"cache-control": "no-store"})
}

// ReceivedTaxDocumentsSyncStatusHandler consulta exclusivamente una solicitud del ambiente actual.
func ReceivedTaxDocumentsSyncStatusHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	id := req.PathParameters["requestId"]
	if req.RequestContext.HTTP.Method != http.MethodGet || !validPortalID(id) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	job, err := (dteinbox.Store{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")}).SyncRequest(ctx, id, time.Now())
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	if job == nil {
		return adminFailure(req, 404, "NOT_FOUND")
	}
	return lambdautil.SuccessResponseWithHeaders(200, map[string]any{"request": job}, map[string]string{"cache-control": "no-store"})
}

// ReceivedTaxDocumentsManualSyncHandler solicita una lectura fuera del horario programado.
func ReceivedTaxDocumentsManualSyncHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	stage, err := currentStage()
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return requestReceivedSync(ctx, req, dteinbox.Store{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")}, lambda.NewFromConfig(cfg), stage, time.Now())
}
