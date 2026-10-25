package functions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

var templateNamePattern = regexp.MustCompile(`^[a-z0-9_]{1,512}$`)
var templateLanguagePattern = regexp.MustCompile(`^[a-z]{2,3}(?:_[A-Z]{2})?$`)

type TemplateLister interface {
	ListApproved(context.Context) ([]domain.MessageTemplate, error)
}
type TemplateSender interface {
	SendTemplate(context.Context, string, string, string, domain.SendTemplate) (domain.SendState, error)
}

func ListTemplates(ctx context.Context, service TemplateLister, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, err := lambdautil.IdentityFromContext(request); err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	items, err := service.ListApproved(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.Internal(err))
	}
	return lambdautil.SuccessResponse(http.StatusOK, items)
}

type sendTemplateRequest struct {
	ClientMessageID string   `json:"clientMessageId"`
	Name            string   `json:"name"`
	Language        string   `json:"language"`
	Parameters      []string `json:"parameters"`
}

func SendTemplate(ctx context.Context, service TemplateSender, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	identity, err := lambdautil.IdentityFromContext(request)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	phone := request.PathParameters["telefono"]
	if !phonePattern.MatchString(phone) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeValidationPhoneInvalid, "El telefono no es valido"))
	}
	var input sendTemplateRequest
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be valid JSON"))
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must contain one JSON object"))
	}
	if !uuidPattern.MatchString(input.ClientMessageID) || !templateNamePattern.MatchString(input.Name) || !templateLanguagePattern.MatchString(input.Language) || len(input.Parameters) > 20 {
		return lambdautil.ErrorResponse(request, invalidListQuery("template", "has invalid fields"))
	}
	for _, value := range input.Parameters {
		if len([]rune(strings.TrimSpace(value))) < 1 || len([]rune(value)) > 1024 {
			return lambdautil.ErrorResponse(request, invalidListQuery("parameters", "must contain non-empty values up to 1024 characters"))
		}
	}
	state, err := service.SendTemplate(ctx, identity.UserID, phone, input.ClientMessageID, domain.SendTemplate{Name: input.Name, Language: input.Language, Parameters: input.Parameters})
	if err != nil {
		if errors.Is(err, domain.ErrTemplateNotApproved) {
			return lambdautil.ErrorResponse(request, invalidListQuery("template", "must be approved, supported, and match its required parameters"))
		}
		if errors.Is(err, domain.ErrSendConflict) {
			return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeInboxIdempotencyConflict, "El identificador ya fue usado con otros datos"))
		}
		if errors.Is(err, domain.ErrProviderRejected) {
			return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeInboxProviderRejected, "WhatsApp rechazo el envio").WithDetails(map[string]any{"sendState": state}))
		}
		return lambdautil.ErrorResponse(request, apperr.Internal(err))
	}
	status := http.StatusAccepted
	if state.State == domain.SendCommitted {
		status = http.StatusOK
	}
	return lambdautil.SuccessResponse(status, state)
}
