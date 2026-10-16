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

type ConversationMutator interface {
	MarkUnread(context.Context, string) (domain.ConversationSummary, error)
	DeleteConversation(context.Context, string) (domain.DeleteConversationResult, error)
}

type markUnreadRequest struct {
	Unread bool `json:"unread"`
}

// MarkConversationUnread validates the exact command and sets unreadCount to one.
func MarkConversationUnread(ctx context.Context, service ConversationMutator, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, err := lambdautil.IdentityFromContext(request); err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	phone := request.PathParameters["telefono"]
	if !phonePattern.MatchString(phone) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeValidationPhoneInvalid, "El telefono no es valido"))
	}
	var input markUnreadRequest
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || !input.Unread {
		return lambdautil.ErrorResponse(request, invalidListQuery("unread", "must be exactly true"))
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must contain one JSON object"))
	}
	result, err := service.MarkUnread(ctx, phone)
	if errors.Is(err, domain.ErrConversationNotFound) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeResourceNotFound, "La conversacion no existe"))
	}
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.Internal(err))
	}
	return lambdautil.SuccessResponse(http.StatusOK, result)
}

// DeleteConversation validates an empty request and performs idempotent logical deletion.
func DeleteConversation(ctx context.Context, service ConversationMutator, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, err := lambdautil.IdentityFromContext(request); err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	phone := request.PathParameters["telefono"]
	if !phonePattern.MatchString(phone) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeValidationPhoneInvalid, "El telefono no es valido"))
	}
	if strings.TrimSpace(request.Body) != "" {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be empty"))
	}
	result, err := service.DeleteConversation(ctx, phone)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.Internal(err))
	}
	return lambdautil.SuccessResponse(http.StatusOK, result)
}
