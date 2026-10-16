package functions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

type SendManager interface {
	Get(context.Context, string, string) (domain.SendState, error)
	Retry(context.Context, string, string) (domain.SendState, error)
	Resolve(context.Context, string, string, string, string, string) (domain.SendState, error)
}

func GetSend(ctx context.Context, service SendManager, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	owner, id, failure := sendIdentity(request)
	if failure != nil {
		return lambdautil.ErrorResponse(request, failure)
	}
	state, err := service.Get(ctx, owner, id)
	if err != nil {
		return lambdautil.ErrorResponse(request, mapSendManagementError(err))
	}
	return lambdautil.SuccessResponse(http.StatusOK, state)
}
func RetrySend(ctx context.Context, service SendManager, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	owner, id, failure := sendIdentity(request)
	if failure != nil {
		return lambdautil.ErrorResponse(request, failure)
	}
	if strings.TrimSpace(request.Body) != "" && strings.TrimSpace(request.Body) != "{}" {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be an empty object"))
	}
	state, err := service.Retry(ctx, owner, id)
	if err != nil {
		mapped := mapSendManagementError(err)
		if errors.Is(err, domain.ErrProviderRejected) {
			mapped = apperr.New(apperr.CodeInboxProviderRejected, "WhatsApp rechazo el envio").WithDetails(map[string]any{"sendState": state})
		}
		return lambdautil.ErrorResponse(request, mapped)
	}
	status := http.StatusAccepted
	if state.State == domain.SendCommitted {
		status = http.StatusOK
	}
	return lambdautil.SuccessResponse(status, state)
}

type resolveSendRequest struct {
	Resolution        string `json:"resolution"`
	WhatsAppMessageID string `json:"whatsappMessageId,omitempty"`
	Reason            string `json:"reason"`
}

func ResolveSend(ctx context.Context, service SendManager, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	owner, id, failure := sendIdentity(request)
	if failure != nil {
		return lambdautil.ErrorResponse(request, failure)
	}
	var input resolveSendRequest
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be a valid resolution"))
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must contain one JSON object"))
	}
	input.Reason = strings.TrimSpace(input.Reason)
	input.WhatsAppMessageID = strings.TrimSpace(input.WhatsAppMessageID)
	if (input.Resolution != "delivered" && input.Resolution != "not_delivered") || len([]rune(input.Reason)) < 1 || len([]rune(input.Reason)) > 500 || (input.Resolution == "delivered" && (len(input.WhatsAppMessageID) < 1 || len(input.WhatsAppMessageID) > 256)) || (input.Resolution == "not_delivered" && input.WhatsAppMessageID != "") {
		return lambdautil.ErrorResponse(request, invalidListQuery("resolution", "must be delivered with a provider id or not_delivered without one"))
	}
	state, err := service.Resolve(ctx, owner, id, input.Resolution, input.WhatsAppMessageID, input.Reason)
	if err != nil {
		return lambdautil.ErrorResponse(request, mapSendManagementError(err))
	}
	return lambdautil.SuccessResponse(http.StatusOK, state)
}

func sendIdentity(request events.APIGatewayV2HTTPRequest) (string, string, error) {
	identity, err := lambdautil.IdentityFromContext(request)
	if err != nil {
		return "", "", apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario")
	}
	id := request.PathParameters["clientMessageId"]
	if !uuidPattern.MatchString(id) {
		return "", "", invalidListQuery("clientMessageId", "must be a UUID")
	}
	return identity.UserID, id, nil
}
func mapSendManagementError(err error) error {
	switch {
	case errors.Is(err, domain.ErrSendNotFound):
		return apperr.New(apperr.CodeResourceNotFound, "El envio no existe")
	case errors.Is(err, domain.ErrSendNotRetryable):
		return apperr.New(apperr.CodeInboxSendNotRetryable, "El envio no se puede reintentar")
	case errors.Is(err, domain.ErrSendRetryExhausted):
		return apperr.New(apperr.CodeInboxSendRetryExhausted, "El reintento ya fue utilizado")
	case errors.Is(err, domain.ErrSendNotResolvable):
		return apperr.New(apperr.CodeInboxSendNotResolvable, "El envio no requiere resolucion")
	default:
		return apperr.Internal(err)
	}
}
