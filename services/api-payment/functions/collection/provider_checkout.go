package collection

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

const reconciliationPendingPK = "RECONCILIATION#PENDING"

func reconciliationJobSK(at time.Time, attemptID string) string {
	return fmt.Sprintf("%020d#%s", at.UTC().Unix(), attemptID)
}

// CheckoutGateway permite probar el envío sin ejecutar cobros reales.
type CheckoutGateway interface {
	DevelopmentBank(context.Context) (string, error)
	Create(context.Context, providers.CheckoutRequest) (providers.Checkout, error)
}

// CheckoutResult representa un enlace creado, nunca un ingreso confirmado.
type CheckoutResult struct {
	PaymentID  string    `json:"paymentId"`
	PaymentURL string    `json:"paymentUrl"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// CheckoutURLs contiene destinos del servidor; nunca se toma del cuerpo público.
type CheckoutURLs struct{ Return, Cancel, Notify string }

var checkoutPaymentID = regexp.MustCompile(`^[a-zA-Z0-9]{12}$`)

func secureCheckoutURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

// CreateDevelopmentCheckout enlaza una reserva con Khipu solo después de verificar DemoBank.
// No realiza reintentos externos. Una interrupción conserva la cuenta bloqueada para conciliación.
// La identidad debe venir de una sesión autorizada; esta función no es un handler público.
func (s Service) CreateDevelopmentCheckout(ctx context.Context, gateway CheckoutGateway, accountID, attemptID, actor string, urls CheckoutURLs, now time.Time) (CheckoutResult, error) {
	if gateway == nil || s.DB == nil || s.Table == "" || accountID == "" || attemptID == "" || actor == "" || now.IsZero() || !secureCheckoutURL(urls.Return) || !secureCheckoutURL(urls.Cancel) || !secureCheckoutURL(urls.Notify) {
		return CheckoutResult{}, domain.ErrInvalid
	}
	a, err := s.GetAccount(ctx, accountID)
	if err != nil {
		return CheckoutResult{}, err
	}
	if !a.Active || a.Free || a.OpenAttemptID != attemptID {
		return CheckoutResult{}, domain.ErrConflict
	}
	r, err := s.read(ctx, "ATTEMPT#"+attemptID, "CHECKOUT")
	if err == nil {
		if r.AccountID != accountID || r.Checkout == nil || !r.Checkout.ExpiresAt.After(now) {
			return CheckoutResult{}, ErrDispatchUncertain
		}
		return *r.Checkout, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return CheckoutResult{}, err
	}
	// Evitar incluso volver a consultar el proveedor cuando ya existe un envío ambiguo.
	if _, err = s.read(ctx, "ATTEMPT#"+attemptID, "DISPATCH"); err == nil {
		return CheckoutResult{}, ErrDispatchUncertain
	} else if !errors.Is(err, ErrNotFound) {
		return CheckoutResult{}, err
	}
	bank, err := gateway.DevelopmentBank(ctx)
	if err != nil || bank == "" {
		return CheckoutResult{}, providers.ErrVerification
	}
	attempt, err := s.ClaimCheckoutDispatch(ctx, accountID, attemptID, actor, now)
	if err != nil {
		return CheckoutResult{}, err
	}
	expires := now.UTC().Add(time.Hour)
	out, err := gateway.Create(ctx, providers.CheckoutRequest{BankID: bank, TransactionID: attempt.ID, Amount: attempt.Amount, Subject: "Cuota de viaje — prueba DEV sin dinero real", ReturnURL: urls.Return, CancelURL: urls.Cancel, NotifyURL: urls.Notify, ExpiresAt: expires})
	if err != nil {
		return CheckoutResult{}, ErrDispatchUncertain
	}
	u, parseErr := url.Parse(out.PaymentURL)
	if parseErr != nil || !secureCheckoutURL(out.PaymentURL) || u.Port() != "" || (u.Hostname() != "khipu.com" && u.Hostname() != "app.khipu.com") || !checkoutPaymentID.MatchString(out.PaymentID) {
		return CheckoutResult{}, ErrDispatchUncertain
	}
	result := CheckoutResult{PaymentID: out.PaymentID, PaymentURL: out.PaymentURL, ExpiresAt: expires}
	row := record{PK: "ATTEMPT#" + attemptID, SK: "CHECKOUT", AccountID: accountID, Checkout: &result, Status: "PENDING_PAYMENT"}
	write, err := s.put(row, "attribute_not_exists(pk)", nil)
	if err != nil {
		return CheckoutResult{}, ErrDispatchUncertain
	}
	firstCheck := now.UTC().Add(time.Minute)
	job, jobErr := s.put(record{PK: reconciliationPendingPK, SK: reconciliationJobSK(firstCheck, attemptID), AttemptID: attemptID, AccountID: accountID, Status: "PENDING", NextAttemptAt: firstCheck.Unix()}, "attribute_not_exists(pk)", nil)
	if jobErr != nil {
		return CheckoutResult{}, ErrDispatchUncertain
	}
	if _, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{write, job}}); err != nil {
		stored, readErr := s.read(ctx, row.PK, row.SK)
		if readErr != nil || stored.AccountID != accountID || stored.Checkout == nil || *stored.Checkout != result {
			return CheckoutResult{}, ErrDispatchUncertain
		}
	}
	return result, nil
}
