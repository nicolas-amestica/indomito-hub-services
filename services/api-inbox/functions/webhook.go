package functions

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

const maxWebhookBody = 1 << 20

type WebhookReceiver interface {
	VerifySignature([]byte, string) bool
	Reserve(context.Context, domain.InboundMetaMessage) error
	ApplyStatus(context.Context, domain.InboundMetaStatus) error
}

func VerifyWhatsAppWebhook(verifyToken string, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	query := request.QueryStringParameters
	if query["hub.mode"] != "subscribe" || query["hub.verify_token"] != verifyToken || query["hub.challenge"] == "" {
		return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusForbidden, Body: "forbidden"}, nil
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusOK, Body: query["hub.challenge"], Headers: map[string]string{"content-type": "text/plain; charset=utf-8", "cache-control": "no-store"}}, nil
}

func ReceiveWhatsAppWebhook(ctx context.Context, service WebhookReceiver, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	raw := []byte(request.Body)
	if request.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(request.Body)
		if err != nil {
			return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be valid base64"))
		}
		raw = decoded
	}
	if len(raw) > maxWebhookBody {
		response, responseErr := lambdautil.ErrorResponse(request, apperr.New(apperr.CodeValidationInputInvalid, "El webhook excede el limite permitido"))
		response.StatusCode = http.StatusRequestEntityTooLarge
		return response, responseErr
	}
	signature := request.Headers["x-hub-signature-256"]
	if signature == "" {
		signature = request.Headers["X-Hub-Signature-256"]
	}
	if !service.VerifySignature(raw, strings.TrimSpace(signature)) {
		return events.APIGatewayV2HTTPResponse{
			StatusCode: http.StatusUnauthorized,
			Body:       "unauthorized",
			Headers:    map[string]string{"content-type": "text/plain; charset=utf-8", "cache-control": "no-store"},
		}, nil
	}
	messages, err := domain.DecodeInboundMetaMessages(raw)
	if err != nil {
		return lambdautil.SuccessResponse(http.StatusOK, map[string]string{"status": "ignored"})
	}
	statuses, err := domain.DecodeInboundMetaStatuses(raw)
	if err != nil || len(messages)+len(statuses) == 0 {
		return lambdautil.SuccessResponse(http.StatusOK, map[string]string{"status": "ignored"})
	}
	for _, status := range statuses {
		if err := service.ApplyStatus(ctx, status); err != nil {
			return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeInboxCapacityUnavailable, "No fue posible procesar el estado de entrega"))
		}
	}
	for _, message := range messages {
		if err := service.Reserve(ctx, message); err != nil {
			return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeInboxCapacityUnavailable, "No fue posible reservar el mensaje entrante"))
		}
	}
	return lambdautil.SuccessResponse(http.StatusOK, map[string]string{"status": "accepted"})
}
