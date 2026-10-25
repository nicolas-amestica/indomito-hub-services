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
	"github.com/oklog/ulid/v2"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type TextSender interface {
	SendTeamText(context.Context, string, string, string, string, string) (domain.SendState, error)
}
type sendReplyRequest struct {
	Content          string `json:"content"`
	ClientMessageID  string `json:"clientMessageId"`
	ReplyToMessageID string `json:"replyToMessageId,omitempty"`
}

func SendReply(ctx context.Context, service TextSender, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	identity, err := lambdautil.IdentityFromContext(request)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	phone := request.PathParameters["telefono"]
	if !phonePattern.MatchString(phone) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeValidationPhoneInvalid, "El telefono no es valido"))
	}
	var input sendReplyRequest
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be valid JSON with only supported fields"))
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must contain one JSON object"))
	}
	input.Content = strings.TrimSpace(input.Content)
	if len([]rune(input.Content)) < 1 || len([]rune(input.Content)) > 4096 {
		return lambdautil.ErrorResponse(request, invalidListQuery("content", "must contain between 1 and 4096 characters"))
	}
	if !uuidPattern.MatchString(input.ClientMessageID) {
		return lambdautil.ErrorResponse(request, invalidListQuery("clientMessageId", "must be a UUID"))
	}
	if input.ReplyToMessageID != "" {
		if _, err = ulid.ParseStrict(input.ReplyToMessageID); err != nil {
			return lambdautil.ErrorResponse(request, invalidListQuery("replyToMessageId", "must be a ULID"))
		}
	}
	state, err := service.SendTeamText(ctx, identity.UserID, phone, input.ClientMessageID, input.Content, input.ReplyToMessageID)
	if err != nil {
		var mapped error
		switch {
		case errors.Is(err, domain.ErrSendConflict):
			mapped = apperr.New(apperr.CodeInboxIdempotencyConflict, "El identificador ya fue usado con otros datos")
		case errors.Is(err, domain.ErrServiceWindow):
			mapped = apperr.New(apperr.CodeInboxServiceWindowClosed, "La ventana de atencion de WhatsApp esta cerrada")
		case errors.Is(err, domain.ErrProviderRejected):
			mapped = apperr.New(apperr.CodeInboxProviderRejected, "WhatsApp rechazo el envio").WithDetails(map[string]any{"sendState": state})
		case errors.Is(err, domain.ErrReplyTargetInvalid):
			mapped = invalidListQuery("replyToMessageId", "must reference a WhatsApp message in this conversation")
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
