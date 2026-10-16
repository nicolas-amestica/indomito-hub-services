package functions

import (
	"context"
	"errors"
	"github.com/aws/aws-lambda-go/events"
	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
	"net/http"
	"strings"
)

type BulkOperator interface {
	Start(context.Context, string, string, domain.BulkKind) (domain.BulkOperation, error)
	Get(context.Context, string) (domain.BulkOperation, error)
	Active(context.Context, domain.BulkKind) (*domain.BulkOperation, string, error)
}

func StartBulk(ctx context.Context, service BulkOperator, kind domain.BulkKind, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	identity, err := lambdautil.IdentityFromContext(request)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	if strings.TrimSpace(request.Body) != "" && strings.TrimSpace(request.Body) != "{}" {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be an empty object"))
	}
	key := request.Headers["idempotency-key"]
	if key == "" {
		key = request.Headers["Idempotency-Key"]
	}
	if !uuidPattern.MatchString(key) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeValidationIdempotencyKeyInvalid, "Idempotency-Key debe ser UUID"))
	}
	operation, err := service.Start(ctx, identity.UserID, key, kind)
	if errors.Is(err, domain.ErrBulkConflict) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeInboxBulkConflict, "La solicitud masiva entra en conflicto con una anterior"))
	}
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.Internal(err))
	}
	return lambdautil.SuccessResponse(http.StatusAccepted, operation)
}
func GetBulkOperation(ctx context.Context, service BulkOperator, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, err := lambdautil.IdentityFromContext(request); err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	id := request.PathParameters["operationId"]
	if _, err := ulid.ParseStrict(id); err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("operationId", "must be a ULID"))
	}
	operation, err := service.Get(ctx, id)
	if errors.Is(err, domain.ErrBulkNotFound) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeResourceNotFound, "La operacion no existe"))
	}
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.Internal(err))
	}
	return lambdautil.SuccessResponse(http.StatusOK, operation)
}
func GetActiveBulkOperation(ctx context.Context, service BulkOperator, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, err := lambdautil.IdentityFromContext(request); err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	kind := domain.BulkKind(request.QueryStringParameters["kind"])
	if (kind != domain.BulkMarkAllRead && kind != domain.BulkRecalculateUnread) || request.QueryStringParameters["state"] != "active" {
		return lambdautil.ErrorResponse(request, invalidListQuery("kind", "kind and state=active are required"))
	}
	operation, _, err := service.Active(ctx, kind)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.Internal(err))
	}
	return lambdautil.SuccessResponse(http.StatusOK, operation)
}
