package collection

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

// PaymentVerifier valida identidad, cobrador, moneda, importe y estado contra el proveedor.
// El adaptador Khipu implementa este contrato; nunca usar datos del navegador como verificador.
type PaymentVerifier interface {
	VerifyReceived(context.Context, string, string) (providers.VerifiedPayment, error)
}

func paymentOperationID(reference string) string {
	hash := sha256.Sum256([]byte("payment-confirmation:v1:" + reference))
	var id ulid.ULID
	copy(id[:], hash[:16])
	return id.String()
}

func (s Service) confirmedPayment(ctx context.Context, commandID, accountID, reference string) (domain.Change, error) {
	r, err := s.read(ctx, "COMMAND#"+commandID, "META")
	if err != nil {
		return domain.Change{}, err
	}
	if r.Change == nil || r.Change.Event.AccountID != accountID || r.Change.Event.Reference != reference || (r.Change.Event.Type != "PAYMENT_RECEIVED" && r.Change.Event.Type != "PAYMENT_REQUIRES_REVIEW") {
		return domain.Change{}, ErrReplayMismatch
	}
	return *r.Change, nil
}

// ConfirmProviderCheckout registra una confirmación verificada sin duplicar ingreso ni comprobante.
// Debe invocarse desde webhook autenticado o proceso interno autorizado, no desde el retorno público.
// Una cuenta inactiva o cuota alterada recibe fondos no aplicados para revisión, no otra cuota pagada.
func (s Service) ConfirmProviderCheckout(ctx context.Context, verifier PaymentVerifier, attemptID, paymentID string, now time.Time, businessZone *time.Location) (domain.Change, error) {
	if s.DB == nil || s.Table == "" || verifier == nil || attemptID == "" || !checkoutPaymentID.MatchString(paymentID) || now.IsZero() || businessZone == nil {
		return domain.Change{}, domain.ErrInvalid
	}
	r, err := s.read(ctx, "ATTEMPT#"+attemptID, "META")
	if err != nil {
		return domain.Change{}, err
	}
	if r.Attempt == nil || r.Attempt.ID != attemptID || r.Attempt.AccountID == "" {
		return domain.Change{}, domain.ErrInvalid
	}
	attempt := *r.Attempt
	if _, err = s.read(ctx, "ATTEMPT#"+attemptID, "DISPATCH"); err != nil {
		return domain.Change{}, err
	}
	reference := "khipu:" + paymentID
	commandID := paymentOperationID(reference)
	if existing, readErr := s.confirmedPayment(ctx, commandID, attempt.AccountID, reference); readErr == nil {
		if existing.Event.AttemptID != attemptID {
			return domain.Change{}, ErrReplayMismatch
		}
		return existing, nil
	} else if !errors.Is(readErr, ErrNotFound) {
		return domain.Change{}, readErr
	}
	// También permite recuperar un pago cuya respuesta de creación no se alcanzó a guardar.
	checkout, err := s.read(ctx, "ATTEMPT#"+attemptID, "CHECKOUT")
	if err == nil && (checkout.Checkout == nil || checkout.Checkout.PaymentID != paymentID || checkout.AccountID != attempt.AccountID) {
		return domain.Change{}, providers.ErrVerification
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return domain.Change{}, err
	}
	verified, err := verifier.VerifyReceived(ctx, paymentID, attemptID)
	if err != nil {
		return domain.Change{}, err
	}
	amount, amountErr := providers.ConfirmedCLPAmount(verified.Amount)
	if amountErr != nil || verified.ReceiverID <= 0 {
		return domain.Change{}, providers.ErrVerification
	}
	if verified.PaymentID != paymentID || verified.TransactionID != attemptID || verified.Currency != "CLP" || verified.Status != "done" || verified.StatusDetail != "normal" || verified.ConciliationDate.IsZero() {
		return domain.Change{}, providers.ErrVerification
	}
	account, err := s.GetAccount(ctx, attempt.AccountID)
	if err != nil {
		return domain.Change{}, err
	}
	audit := domain.Audit{CommandID: commandID, Actor: "provider:khipu", Reason: "Pago confirmado mediante API del proveedor", RecordedAt: now.UTC()}
	command := Command{ID: commandID, AccountID: account.ID, ExpectedVersion: account.Version, Reference: reference, ReceiptID: commandID, Payload: verified}
	command.ConfirmedAttemptID = attemptID
	command.ReceiptEmail = attempt.Email
	change, err := s.Apply(ctx, command, func(current domain.Account) (domain.Change, error) {
		return domain.ConfirmPayment(current, audit, attempt, amount, reference, verified.ConciliationDate.In(businessZone).Format("2006-01-02"), false)
	})
	if err != nil {
		// Otro webhook puede haber confirmado mientras se verificaba contra Khipu.
		if existing, readErr := s.confirmedPayment(ctx, commandID, attempt.AccountID, reference); readErr == nil {
			if existing.Event.AttemptID != attemptID {
				return domain.Change{}, ErrReplayMismatch
			}
			return existing, nil
		}
	}
	return change, err
}
