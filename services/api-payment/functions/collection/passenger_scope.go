package collection

import (
	"context"

	"github.com/aws/aws-lambda-go/events"
	"github.com/oklog/ulid/v2"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// PassengerAccount consume solo contexto verificado por el authorizer, no cabeceras o cuerpo.
// Revalida código vigente y pertenencia para invalidar sesiones de códigos revocados.
func (s Service) PassengerAccount(ctx context.Context, req events.APIGatewayV2HTTPRequest) (domain.Account, error) {
	if req.RequestContext.Authorizer == nil {
		return domain.Account{}, ErrNotFound
	}
	claims := req.RequestContext.Authorizer.Lambda
	if claims["paymentAccess"] != "passenger" {
		return domain.Account{}, ErrNotFound
	}
	accountID, _ := claims["paymentAccountId"].(string)
	tripID, _ := claims["paymentTripId"].(string)
	sessionID, _ := claims["paymentSessionId"].(string)
	codeKey, _ := claims["paymentCodeKey"].(string)
	for _, id := range []string{accountID, tripID, sessionID} {
		parsed, err := ulid.ParseStrict(id)
		if err != nil || parsed.String() != id {
			return domain.Account{}, ErrNotFound
		}
	}
	if !passengerCodeKey.MatchString(codeKey) {
		return domain.Account{}, ErrNotFound
	}
	code, err := s.read(ctx, codeKey, "META")
	if err != nil {
		return domain.Account{}, err
	}
	if code.Status != "RESERVED_APPROVED" || code.TripID != tripID {
		return domain.Account{}, ErrNotFound
	}
	a, err := s.GetAccount(ctx, accountID)
	if err != nil {
		return domain.Account{}, err
	}
	if a.TripID != tripID {
		return domain.Account{}, ErrNotFound
	}
	return a, nil
}
