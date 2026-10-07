package collection

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// ErrDispatchUncertain obliga a conciliar antes de cualquier nueva creación externa.
var ErrDispatchUncertain = errors.New("envio iniciado o incierto; requiere conciliacion")

// ClaimCheckoutDispatch concede una sola autorización de envío por intento reservado.
// Solo un retorno sin error habilita la llamada externa en esta misma ejecución.
// Un timeout, incluso después del commit, NO se recupera como permiso de envío.
// El registro es permanente: no usar TTL ni eliminarlo para reintentar Khipu.
func (s Service) ClaimCheckoutDispatch(ctx context.Context, accountID, attemptID, actor string, now time.Time) (domain.Attempt, error) {
	if s.DB == nil || s.Table == "" || accountID == "" || attemptID == "" || actor == "" || now.IsZero() {
		return domain.Attempt{}, domain.ErrInvalid
	}
	r, err := s.read(ctx, "ATTEMPT#"+attemptID, "META")
	if err != nil {
		return domain.Attempt{}, err
	}
	if r.Attempt == nil || r.Attempt.ID != attemptID || r.Attempt.AccountID != accountID || r.Attempt.Status != "CREATING" {
		return domain.Attempt{}, domain.ErrInvalid
	}
	a, err := s.GetAccount(ctx, accountID)
	if err != nil {
		return domain.Attempt{}, err
	}
	if !a.Active || a.Free || a.OpenAttemptID != attemptID {
		return domain.Attempt{}, domain.ErrConflict
	}
	// Revalidar importe y orden contra la cuenta actual sin modificar la reserva.
	check := a
	check.OpenAttemptID = ""
	audit := domain.Audit{CommandID: attemptID, Actor: actor, Reason: "Autorización única de envío al proveedor", RecordedAt: now.UTC()}
	_, expected, err := domain.OpenAttempt(check, audit, attemptID, r.Attempt.Email)
	if err != nil || expected != *r.Attempt {
		return domain.Attempt{}, domain.ErrConflict
	}
	event := domain.Event{Audit: audit, SchemaVersion: 1, Type: "CHECKOUT_DISPATCH_CLAIMED", AccountID: a.ID, TripID: a.TripID, Amount: expected.Amount, Reference: attemptID}
	claim, err := s.put(record{PK: "ATTEMPT#" + attemptID, SK: "DISPATCH", Status: "RECONCILIATION_REQUIRED", Event: &event}, "attribute_not_exists(pk)", nil)
	if err != nil {
		return domain.Attempt{}, err
	}
	// CAS sin cambio financiero: evita conceder envío sobre una cuenta modificada.
	guard, err := s.put(record{PK: "ACCOUNT#" + a.ID, SK: "META", Version: a.Version, Account: &a}, "#version = :previous", map[string]types.AttributeValue{":previous": &types.AttributeValueMemberN{Value: strconv.FormatInt(a.Version, 10)}})
	if err != nil {
		return domain.Attempt{}, err
	}
	if _, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{guard, claim}}); err != nil {
		return domain.Attempt{}, ErrDispatchUncertain
	}
	return expected, nil
}
