package functions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

var supportedReactions = map[string]struct{}{"👍": {}, "❤️": {}, "😂": {}, "😮": {}, "😢": {}, "🙏": {}}

type ReactionSender interface {
	React(context.Context, string, string, string) error
}
type reactionRequest struct {
	MessageID string `json:"messageId"`
	Emoji     string `json:"emoji"`
}

func SendReaction(ctx context.Context, service ReactionSender, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, err := lambdautil.IdentityFromContext(request); err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	phone := request.PathParameters["telefono"]
	if !phonePattern.MatchString(phone) {
		return lambdautil.ErrorResponse(request, invalidListQuery("telefono", "must be a valid phone"))
	}
	var input reactionRequest
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be valid JSON"))
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must contain one JSON object"))
	}
	_, supported := supportedReactions[input.Emoji]
	if _, err := ulid.ParseStrict(input.MessageID); err != nil || (input.Emoji != "" && !supported) {
		return lambdautil.ErrorResponse(request, invalidListQuery("reaction", "has invalid fields"))
	}
	if err := service.React(ctx, phone, input.MessageID, input.Emoji); err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("messageId", "must reference a WhatsApp message in this conversation"))
	}
	return lambdautil.SuccessResponse(http.StatusOK, map[string]any{"messageId": input.MessageID, "emoji": input.Emoji})
}
