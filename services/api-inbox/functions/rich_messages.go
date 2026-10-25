package functions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/mail"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

type RichMessageSender interface {
	SendRichMessage(context.Context, string, string, string, domain.MessageRichContent) (domain.SendState, error)
}

type sendRichMessageRequest struct {
	ClientMessageID string                     `json:"clientMessageId"`
	Kind            string                     `json:"kind"`
	Location        *domain.MessageLocation    `json:"location,omitempty"`
	Contacts        []domain.MessageContact    `json:"contacts,omitempty"`
	Interactive     *domain.MessageInteractive `json:"interactive,omitempty"`
}

func SendRichMessage(ctx context.Context, service RichMessageSender, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	identity, err := lambdautil.IdentityFromContext(request)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	phone := request.PathParameters["telefono"]
	if !phonePattern.MatchString(phone) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeValidationPhoneInvalid, "El telefono no es valido"))
	}
	var input sendRichMessageRequest
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be valid JSON with only supported fields"))
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must contain one JSON object"))
	}
	if !uuidPattern.MatchString(input.ClientMessageID) || !validRichMessage(input) {
		return lambdautil.ErrorResponse(request, invalidListQuery("richMessage", "must contain a valid location, contact list, or interactive message"))
	}
	rich := domain.MessageRichContent{Kind: input.Kind, Location: input.Location, Contacts: input.Contacts, Interactive: input.Interactive}
	state, err := service.SendRichMessage(ctx, identity.UserID, phone, input.ClientMessageID, rich)
	if err != nil {
		var mapped error
		switch {
		case errors.Is(err, domain.ErrSendConflict):
			mapped = apperr.New(apperr.CodeInboxIdempotencyConflict, "El identificador ya fue usado con otros datos")
		case errors.Is(err, domain.ErrServiceWindow):
			mapped = apperr.New(apperr.CodeInboxServiceWindowClosed, "La ventana de atencion de WhatsApp esta cerrada")
		case errors.Is(err, domain.ErrProviderRejected):
			mapped = apperr.New(apperr.CodeInboxProviderRejected, "WhatsApp rechazo el envio").WithDetails(map[string]any{"sendState": state})
		default:
			mapped = apperr.Internal(err)
		}
		return lambdautil.ErrorResponse(request, mapped)
	}
	status := http.StatusAccepted
	if state.State == domain.SendCommitted {
		status = http.StatusOK
	}
	return lambdautil.SuccessResponse(status, state)
}

func validRichMessage(input sendRichMessageRequest) bool {
	if input.Kind == "location" {
		location := input.Location
		return location != nil && len(input.Contacts) == 0 && input.Interactive == nil && !math.IsNaN(location.Latitude) && !math.IsNaN(location.Longitude) && location.Latitude >= -90 && location.Latitude <= 90 && location.Longitude >= -180 && location.Longitude <= 180 && len([]rune(location.Name)) <= 1000 && len([]rune(location.Address)) <= 1000
	}
	if input.Kind == "interactive" {
		return input.Location == nil && len(input.Contacts) == 0 && validInteractive(input.Interactive)
	}
	if input.Kind != "contacts" || input.Location != nil || input.Interactive != nil || len(input.Contacts) < 1 || len(input.Contacts) > 10 {
		return false
	}
	for _, contact := range input.Contacts {
		if strings.TrimSpace(contact.Name) == "" || len([]rune(contact.Name)) > 200 || !phonePattern.MatchString(strings.TrimPrefix(strings.TrimSpace(contact.Phone), "+")) || len([]rune(contact.Company)) > 200 {
			return false
		}
		if contact.Email != "" {
			address, err := mail.ParseAddress(contact.Email)
			if err != nil || address.Address != contact.Email || len(contact.Email) > 254 {
				return false
			}
		}
	}
	return true
}

func validInteractive(interactive *domain.MessageInteractive) bool {
	if interactive == nil || strings.TrimSpace(interactive.Body) == "" || len([]rune(interactive.Body)) > 1024 || len([]rune(interactive.Footer)) > 60 {
		return false
	}
	maximum := 3
	if interactive.Kind == "list" {
		maximum = 10
		if strings.TrimSpace(interactive.Button) == "" || len([]rune(interactive.Button)) > 20 {
			return false
		}
	} else if interactive.Kind != "button" || interactive.Button != "" {
		return false
	}
	if len(interactive.Options) < 1 || len(interactive.Options) > maximum {
		return false
	}
	seen := map[string]bool{}
	for _, option := range interactive.Options {
		if strings.TrimSpace(option.ID) == "" || seen[option.ID] || len([]rune(option.ID)) > 256 || strings.TrimSpace(option.Title) == "" || len([]rune(option.Title)) > 24 || len([]rune(option.Description)) > 72 || (interactive.Kind == "button" && option.Description != "") {
			return false
		}
		seen[option.ID] = true
	}
	return true
}
