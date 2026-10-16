package functions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

// ConversationLister expone únicamente las operaciones requeridas por GET /conversaciones.
type ConversationLister interface {
	FirstPage(context.Context, string, string) (domain.ConversationListResponse, error)
	NextPage(context.Context, string, string, string) (domain.ConversationListResponse, error)
}

// ListConversations valida la solicitud y devuelve la colección sin envelope adicional.
func ListConversations(ctx context.Context, service ConversationLister, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	identity, err := lambdautil.IdentityFromContext(request)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	page, query, cursor, err := listQuery(request.QueryStringParameters)
	if err != nil {
		return lambdautil.ErrorResponse(request, err)
	}
	var response domain.ConversationListResponse
	if page == 1 {
		response, err = service.FirstPage(ctx, identity.UserID, query)
	} else {
		response, err = service.NextPage(ctx, identity.UserID, query, cursor)
	}
	if err != nil {
		return lambdautil.ErrorResponse(request, mapListError(err))
	}
	body, err := json.Marshal(response)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.Internal(err))
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusOK, Headers: map[string]string{"content-type": "application/json", "cache-control": "no-store"}, Body: string(body)}, nil
}

func listQuery(values map[string]string) (int, string, string, error) {
	page := 1
	if raw := strings.TrimSpace(values["page"]); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return 0, "", "", invalidListQuery("page", "must be an integer greater than or equal to 1")
		}
		page = parsed
	}
	if raw := strings.TrimSpace(values["pageSize"]); raw != "" && raw != strconv.Itoa(domain.ConversationListPageSize) {
		return 0, "", "", invalidListQuery("pageSize", "must be 200")
	}
	query, present := values["q"]
	query = strings.TrimSpace(query)
	if present && (len(query) < 1 || len(query) > 100) {
		return 0, "", "", invalidListQuery("q", "must contain between 1 and 100 characters")
	}
	cursor := values["cursor"]
	if (page == 1 && cursor != "") || (page >= 2 && cursor == "") || len(cursor) > 2048 {
		return 0, "", "", invalidListQuery("cursor", "must be present only from page 2")
	}
	return page, query, cursor, nil
}

func invalidListQuery(field, reason string) error {
	return apperr.New(apperr.CodeValidationInputInvalid, "Los parametros de la solicitud no son validos").WithDetails(map[string]any{"field": field, "reason": reason})
}

func mapListError(err error) error {
	switch {
	case errors.Is(err, domain.ErrInvalidCursor):
		return apperr.New(apperr.CodeValidationCursorInvalid, "El cursor no es valido")
	case errors.Is(err, domain.ErrExpiredCursor):
		return apperr.New(apperr.CodeInboxCursorExpired, "El cursor expiro")
	case errors.Is(err, domain.ErrSnapshotLimitReached):
		return apperr.New(apperr.CodeInboxSnapshotLimitReached, "Se alcanzo el limite de sesiones de lista")
	case errors.Is(err, domain.ErrListCapacityExceeded):
		return apperr.New(apperr.CodeInboxCapacityUnavailable, "La bandeja excede temporalmente la capacidad disponible")
	default:
		return apperr.Internal(err)
	}
}
