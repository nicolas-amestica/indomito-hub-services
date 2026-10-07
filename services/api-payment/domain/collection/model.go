// Package collection define transiciones financieras independientes del proveedor y la persistencia.
package collection

import (
	"errors"
	"time"
)

// MaxAmount limita CLP para evitar desbordamiento y errores de entrada.
const MaxAmount int64 = 1_000_000_000_000

// Errores estables para mapear conflictos y entradas inválidas sin filtrar datos privados.
var (
	ErrInvalid  = errors.New("operacion financiera invalida")
	ErrConflict = errors.New("la cuenta cambio o mantiene un cobro sin resolver")
)

// Installment conserva el valor nominal; ajustes y cobros nunca lo sobrescriben.
type Installment struct {
	ID        string `json:"id"`
	DueDate   string `json:"dueDate"`
	Original  int64  `json:"original"`
	Discount  int64  `json:"discount"`
	Cancelled int64  `json:"cancelled"`
	Paid      int64  `json:"paid"`
}

// Outstanding devuelve exclusivamente la deuda exigible de la obligación.
func (i Installment) Outstanding() int64 { return i.Original - i.Discount - i.Cancelled - i.Paid }

// Account es una participación contractual. No contiene historial ilimitado ni datos del pagador.
type Account struct {
	ID                       string        `json:"id"`
	TripID                   string        `json:"tripId"`
	ParticipantID            string        `json:"participantId"`
	Version                  int64         `json:"version"`
	Active                   bool          `json:"active"`
	Free                     bool          `json:"free"`
	DepositAgreed            int64         `json:"depositAgreed"`
	DepositReceived          int64         `json:"depositReceived"`
	Installments             []Installment `json:"installments"`
	OpenAttemptID            string        `json:"openAttemptId,omitempty"`
	ReviewAttemptID          string        `json:"reviewAttemptId,omitempty"`
	UnappliedReceived        int64         `json:"unappliedReceived"`
	ReviewedUnapplied        int64         `json:"reviewedUnapplied,omitempty"`
	WithdrawalRefundApproved int64         `json:"withdrawalRefundApproved"`
	UnappliedRefundApproved  int64         `json:"unappliedRefundApproved"`
	Refunded                 int64         `json:"refunded"`
}

// RequiresPaymentReview bloquea nuevos cobros hasta una resolución financiera explícita.
// El saldo acumulado protege registros anteriores sin ReviewAttemptID; una aprobación
// de devolución no demuestra por sí sola que los fondos hayan sido resueltos.
func (a Account) RequiresPaymentReview() bool {
	return a.ReviewAttemptID != "" || a.UnappliedReceived > a.ReviewedUnapplied
}

// Attempt congela cuota, importe y destinatario del comprobante antes de llamar al banco.
type Attempt struct {
	ID            string `json:"id"`
	AccountID     string `json:"accountId"`
	InstallmentID string `json:"installmentId"`
	Amount        int64  `json:"amount"`
	Email         string `json:"email"`
	Status        string `json:"status"`
}

// Audit acompaña todo comando; el ID y su huella se deduplican durablemente al persistir.
type Audit struct {
	CommandID  string    `json:"commandId"`
	Actor      string    `json:"actor"`
	Reason     string    `json:"reason"`
	RecordedAt time.Time `json:"recordedAt"`
}

// Event es la salida versionada del dominio para auditoría, outbox y futura BI.
type Event struct {
	Audit
	SchemaVersion int     `json:"schemaVersion"`
	Type          string  `json:"type"`
	AccountID     string  `json:"accountId"`
	TripID        string  `json:"tripId"`
	Amount        int64   `json:"amount"`
	Basis         int64   `json:"basis"`
	BasisPoints   int64   `json:"basisPoints"`
	Reference     string  `json:"reference,omitempty"`
	AttemptID     string  `json:"attemptId,omitempty"`
	EffectiveDate string  `json:"effectiveDate,omitempty"`
	Entries       []Entry `json:"entries,omitempty"`
}

// Entry es una partida operativa: débitos positivos, créditos negativos, siempre CLP.
// No equivale por sí sola a contabilidad tributaria ni a reconocimiento de ingresos.
type Entry struct {
	Account string `json:"account"`
	Amount  int64  `json:"amount"`
}

// Change agrupa estado nuevo y evento; ambos deben persistirse en una sola transacción.
type Change struct {
	Account Account `json:"account"`
	Event   Event   `json:"event"`
}

func begin(a Account, audit Audit, kind string) (Change, error) {
	if audit.CommandID == "" || audit.Actor == "" || audit.Reason == "" || audit.RecordedAt.IsZero() || a.Validate() != nil {
		return Change{}, ErrInvalid
	}
	a.Installments = append([]Installment(nil), a.Installments...)
	a.Version++
	return Change{Account: a, Event: Event{Audit: audit, SchemaVersion: 1, Type: kind, AccountID: a.ID, TripID: a.TripID}}, nil
}

// Validate comprueba invariantes antes de aceptar o persistir una transición.
func (a Account) Validate() error {
	if a.ID == "" || a.TripID == "" || a.ParticipantID == "" || a.Version < 1 || len(a.Installments) > 120 {
		return ErrInvalid
	}
	for _, n := range []int64{a.DepositAgreed, a.DepositReceived, a.UnappliedReceived, a.ReviewedUnapplied, a.WithdrawalRefundApproved, a.UnappliedRefundApproved, a.Refunded} {
		if n < 0 || n > MaxAmount {
			return ErrInvalid
		}
	}
	if a.ReviewedUnapplied > a.UnappliedReceived || a.ReviewedUnapplied > a.UnappliedRefundApproved || a.ReviewedUnapplied > a.Refunded {
		return ErrInvalid
	}
	if a.DepositReceived > a.DepositAgreed || a.UnappliedRefundApproved > a.UnappliedReceived || a.Refunded > a.WithdrawalRefundApproved+a.UnappliedRefundApproved {
		return ErrInvalid
	}
	var paid, original int64
	seen := map[string]bool{}
	previous := ""
	for _, i := range a.Installments {
		if i.ID == "" || seen[i.ID] || !validDate(i.DueDate) || i.DueDate < previous {
			return ErrInvalid
		}
		seen[i.ID], previous = true, i.DueDate
		for _, n := range []int64{i.Original, i.Discount, i.Cancelled, i.Paid} {
			if n < 0 || n > MaxAmount {
				return ErrInvalid
			}
		}
		if i.Outstanding() < 0 || (!a.Active && i.Outstanding() != 0) {
			return ErrInvalid
		}
		paid += i.Paid
		original += i.Original
	}
	if original > MaxAmount || paid < a.WithdrawalRefundApproved || (a.Free && (original != 0 || a.DepositAgreed != 0)) {
		return ErrInvalid
	}
	return nil
}

func validDate(date string) bool { _, err := time.Parse(time.DateOnly, date); return err == nil }

func finish(c Change) (Change, error) {
	if err := c.Account.Validate(); err != nil {
		return Change{}, err
	}
	var sum int64
	for _, e := range c.Event.Entries {
		if e.Account == "" || e.Amount > MaxAmount || e.Amount < -MaxAmount {
			return Change{}, ErrInvalid
		}
		sum += e.Amount
	}
	if sum != 0 {
		return Change{}, ErrInvalid
	}
	return c, nil
}
