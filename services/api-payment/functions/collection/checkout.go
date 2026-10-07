package collection

import (
	"context"
	"errors"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// ReserveCheckout persiste la cuota completa y su bloqueo antes de llamar al proveedor.
// Los identificadores y la auditoría deben construirse en el servidor autorizado.
// Un replay recupera la reserva; NO autoriza repetir la creación externa en Khipu.
func (s Service) ReserveCheckout(ctx context.Context, accountID string, version int64, audit domain.Audit, attemptID, sessionID, email string) (domain.Attempt, error) {
	if s.DB == nil || s.Table == "" || accountID == "" || version < 1 || audit.CommandID == "" || attemptID == "" || !validPortalID(sessionID) {
		return domain.Attempt{}, domain.ErrInvalid
	}
	command := Command{ID: audit.CommandID, AccountID: accountID, ExpectedVersion: version,
		Payload: struct {
			Audit     domain.Audit
			AttemptID string
			Email     string
			SessionID string
		}{audit, attemptID, email, sessionID}, PaymentSessionID: sessionID}
	// La entrada estable permite recuperar la reserva aun si la cuenta ya cambió.
	stored, err := s.read(ctx, "COMMAND#"+command.ID, "META")
	if err == nil {
		attempt, readErr := s.read(ctx, "ATTEMPT#"+attemptID, "META")
		if readErr != nil {
			return domain.Attempt{}, readErr
		}
		if attempt.Attempt == nil || attempt.Attempt.AccountID != accountID || attempt.Attempt.ID != attemptID || attempt.PaymentSessionID != sessionID {
			return domain.Attempt{}, domain.ErrInvalid
		}
		command.Attempt = attempt.Attempt
		hash, hashErr := fingerprint(command)
		if hashErr != nil {
			return domain.Attempt{}, hashErr
		}
		if _, replayErr := replay(stored, hash); replayErr != nil {
			return domain.Attempt{}, replayErr
		}
		return *attempt.Attempt, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return domain.Attempt{}, err
	}
	account, err := s.GetAccount(ctx, accountID)
	if err != nil {
		return domain.Attempt{}, err
	}
	_, attempt, err := domain.OpenAttempt(account, audit, attemptID, email)
	if err != nil {
		return domain.Attempt{}, err
	}
	command.Attempt = &attempt
	_, err = s.Apply(ctx, command, func(current domain.Account) (domain.Change, error) {
		change, _, transitionErr := domain.OpenAttempt(current, audit, attemptID, email)
		return change, transitionErr
	})
	return attempt, err
}
