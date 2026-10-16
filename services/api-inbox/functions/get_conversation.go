package functions

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

var phonePattern = regexp.MustCompile(`^[0-9]{8,15}$`)

type ConversationDetailer interface {
	Page(context.Context, string, string, string, int) (domain.ConversationDetailResponse, error)
	Delta(context.Context, string, string, time.Time) (domain.ConversationDetailResponse, error)
}

func GetConversation(ctx context.Context, service ConversationDetailer, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	identity, err := lambdautil.IdentityFromContext(request)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	phone := strings.TrimSpace(request.PathParameters["telefono"])
	if !phonePattern.MatchString(phone) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeValidationPhoneInvalid, "El telefono no es valido"))
	}
	pageSize := 50
	if raw := strings.TrimSpace(request.QueryStringParameters["pageSize"]); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil || pageSize < 1 || pageSize > 50 {
			return lambdautil.ErrorResponse(request, invalidListQuery("pageSize", "must be between 1 and 50"))
		}
	}
	before, since := request.QueryStringParameters["before"], request.QueryStringParameters["since"]
	if before != "" && since != "" {
		return lambdautil.ErrorResponse(request, invalidListQuery("before", "cannot be combined with since"))
	}
	var response domain.ConversationDetailResponse
	if since != "" {
		parsed, parseErr := time.Parse(time.RFC3339, since)
		if parseErr != nil {
			return lambdautil.ErrorResponse(request, invalidListQuery("since", "must be RFC3339"))
		}
		response, err = service.Delta(ctx, identity.UserID, phone, parsed)
	} else {
		response, err = service.Page(ctx, identity.UserID, phone, before, pageSize)
	}
	if err != nil {
		return lambdautil.ErrorResponse(request, mapListError(err))
	}
	return lambdautil.SuccessResponseWithHeaders(http.StatusOK, response, map[string]string{"cache-control": "no-store"})
}
