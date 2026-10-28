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

type AttachmentSender interface {
	Send(context.Context, string, string, string, string, string, string) (domain.SendState, error)
	SendSticker(context.Context, string, string, string, string, string) (domain.SendState, error)
}
type sendAttachmentRequest struct {
	UploadID        string `json:"uploadId"`
	Key             string `json:"key"`
	Caption         string `json:"caption"`
	ClientMessageID string `json:"clientMessageId"`
	Kind            string `json:"kind,omitempty"`
}

func SendAttachment(ctx context.Context, service AttachmentSender, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	identity, err := lambdautil.IdentityFromContext(request)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	phone := request.PathParameters["telefono"]
	if !phonePattern.MatchString(phone) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeValidationPhoneInvalid, "El telefono no es valido"))
	}
	var input sendAttachmentRequest
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be valid JSON"))
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must contain one JSON object"))
	}
	input.Caption = strings.TrimSpace(input.Caption)
	if !uuidPattern.MatchString(input.UploadID) || !uuidPattern.MatchString(input.ClientMessageID) || input.Key == "" || len([]rune(input.Caption)) > 1024 || (input.Kind != "" && input.Kind != "sticker") || (input.Kind == "sticker" && input.Caption != "") {
		return lambdautil.ErrorResponse(request, invalidListQuery("attachment", "uploadId, key, caption or clientMessageId is invalid"))
	}
	var state domain.SendState
	if input.Kind == "sticker" {
		state, err = service.SendSticker(ctx, identity.UserID, phone, input.ClientMessageID, input.UploadID, input.Key)
	} else {
		state, err = service.Send(ctx, identity.UserID, phone, input.ClientMessageID, input.UploadID, input.Key, input.Caption)
	}
	if err != nil {
		var mapped error
		switch {
		case errors.Is(err, domain.ErrUploadInvalid):
			mapped = invalidListQuery("attachment", "upload reservation or object is invalid")
		case errors.Is(err, domain.ErrUnsupportedAttachment):
			mapped = invalidListQuery("attachment", "WhatsApp no admite este archivo; usa PNG de 8 bits o WebP mediante la accion de sticker")
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
