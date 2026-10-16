package functions

import (
	"context"
	"errors"
	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
	"net/http"
)

type SubscriptionCreator interface {
	Create(context.Context, string, string, string) (domain.Subscription, error)
}

func CreateSubscription(ctx context.Context, service SubscriptionCreator, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	identity, err := lambdautil.IdentityFromContext(request)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	scope := request.QueryStringParameters["scope"]
	phone := request.QueryStringParameters["phone"]
	if (scope != "list" && scope != "detail") || (scope == "list" && phone != "") || (scope == "detail" && !phonePattern.MatchString(phone)) {
		return lambdautil.ErrorResponse(request, invalidListQuery("scope", "list forbids phone and detail requires a valid phone"))
	}
	subscription, err := service.Create(ctx, identity.UserID, scope, phone)
	if errors.Is(err, domain.ErrSubscriptionRateLimited) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeInboxSnapshotLimitReached, "Se alcanzo el limite temporal de suscripciones"))
	}
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.Internal(err))
	}
	return lambdautil.SuccessResponseWithHeaders(http.StatusOK, subscription, map[string]string{"cache-control": "no-store"})
}
