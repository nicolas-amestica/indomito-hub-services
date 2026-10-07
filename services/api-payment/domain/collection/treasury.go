package collection

// Position separa deuda contractual, fondos del cliente y obligaciones de devolución.
type Position struct {
	Receivable            int64 `json:"receivable"`
	DepositReceivable     int64 `json:"depositReceivable"`
	InstallmentReceivable int64 `json:"installmentReceivable"`
	AppliedReceipts       int64 `json:"appliedReceipts"`
	UnappliedReceipts     int64 `json:"unappliedReceipts"`
	Discounts             int64 `json:"discounts"`
	Cancelled             int64 `json:"cancelled"`
	RefundPayable         int64 `json:"refundPayable"`
	Refunded              int64 `json:"refunded"`
}

// Position calcula proyecciones verificables sin confundir cobros con efectivo bancario.
func (a Account) Position() Position {
	p := Position{AppliedReceipts: a.DepositReceived, UnappliedReceipts: a.UnappliedReceived, RefundPayable: a.WithdrawalRefundApproved + a.UnappliedRefundApproved - a.Refunded, Refunded: a.Refunded}
	if a.Active {
		p.DepositReceivable = a.DepositAgreed - a.DepositReceived
	} else {
		p.Cancelled = a.DepositAgreed - a.DepositReceived
	}
	for _, i := range a.Installments {
		p.InstallmentReceivable += i.Outstanding()
		p.AppliedReceipts += i.Paid
		p.Discounts += i.Discount
		p.Cancelled += i.Cancelled
	}
	p.Receivable = p.DepositReceivable + p.InstallmentReceivable
	return p
}

// Settlement mantiene cuánto de un cobro verificado se concilió, sin estimar comisiones.
type Settlement struct {
	PaymentReference string `json:"paymentReference"`
	TripID           string `json:"tripId"`
	Amount           int64  `json:"amount"`
	SettledGross     int64  `json:"settledGross"`
	ActualFees       int64  `json:"actualFees"`
	Version          int64  `json:"version"`
}

// ReconcileSettlement convierte fondos en tránsito a banco y comisión respaldada por evidencia.
// Debe persistirse junto al evento y la referencia bancaria única, bajo condición de versión.
func ReconcileSettlement(s Settlement, audit Audit, gross, fee int64, bankReference, effectiveDate string) (Settlement, Event, error) {
	if s.PaymentReference == "" || s.TripID == "" || s.Version < 1 || s.Amount < 1 || s.Amount > MaxAmount || s.SettledGross < 0 || s.SettledGross > s.Amount || s.ActualFees < 0 || s.ActualFees > s.SettledGross || gross <= 0 || gross > s.Amount-s.SettledGross || fee < 0 || fee > gross || bankReference == "" || !validDate(effectiveDate) || !validAudit(audit) {
		return Settlement{}, Event{}, ErrInvalid
	}
	s.SettledGross += gross
	s.ActualFees += fee
	s.Version++
	e := Event{Audit: audit, SchemaVersion: 1, Type: "PAYMENT_SETTLED", TripID: s.TripID, Amount: gross, Reference: bankReference, EffectiveDate: effectiveDate, Entries: []Entry{{"BANK", gross - fee}, {"PAYMENT_FEES", fee}, {"PROVIDER_RECEIVABLE", -gross}}}
	return s, e, nil
}

// SupplierCommitment conserva el compromiso vigente y sus anticipos, separados de recuperaciones pendientes.
type SupplierCommitment struct {
	ID             string `json:"id"`
	TripID         string `json:"tripId"`
	Name           string `json:"name"`
	Service        string `json:"service"`
	Version        int64  `json:"version"`
	Committed      int64  `json:"committed"`
	Paid           int64  `json:"paid"`
	RefundAgreed   int64  `json:"refundAgreed"`
	RefundReceived int64  `json:"refundReceived"`
}

func (s SupplierCommitment) valid() bool {
	return s.ID != "" && s.TripID != "" && len(s.Name) >= 2 && len(s.Name) <= 150 && len(s.Service) >= 2 && len(s.Service) <= 250 && s.Version > 0 && s.Committed >= 0 && s.Committed <= MaxAmount && s.Paid >= 0 && s.Paid <= MaxAmount && s.RefundAgreed >= 0 && s.RefundAgreed <= s.Paid && s.RefundReceived >= 0 && s.RefundReceived <= s.RefundAgreed
}
func validAudit(a Audit) bool {
	return a.CommandID != "" && a.Actor != "" && a.Reason != "" && !a.RecordedAt.IsZero()
}

// CreateSupplierCommitment registra una obligación operativa, no una salida bancaria.
func CreateSupplierCommitment(id, tripID, name, service string, committed int64, audit Audit) (SupplierCommitment, Event, error) {
	s := SupplierCommitment{ID: id, TripID: tripID, Name: name, Service: service, Version: 1, Committed: committed}
	if !s.valid() || !validAudit(audit) || committed <= 0 {
		return SupplierCommitment{}, Event{}, ErrInvalid
	}
	return s, Event{Audit: audit, SchemaVersion: 1, Type: "SUPPLIER_COMMITMENT_CREATED", TripID: tripID, Amount: committed, Reference: id}, nil
}

// ReviseSupplier registra un acuerdo real de servicios y devolución. No supone dinero recuperado.
func ReviseSupplier(s SupplierCommitment, audit Audit, committed, refundAgreed int64, annexID string) (SupplierCommitment, Event, error) {
	if !s.valid() || !validAudit(audit) || annexID == "" || committed < 0 || committed > MaxAmount || refundAgreed < s.RefundAgreed || refundAgreed > s.Paid {
		return SupplierCommitment{}, Event{}, ErrInvalid
	}
	e := Event{Audit: audit, SchemaVersion: 1, Type: "SUPPLIER_COMMITMENT_REVISED", TripID: s.TripID, Amount: committed - s.Committed, Basis: s.Committed, Reference: annexID}
	s.Committed = committed
	s.RefundAgreed = refundAgreed
	s.Version++
	return s, e, nil
}

// PaySupplier registra una salida real hasta el compromiso pendiente, con evidencia bancaria única.
func PaySupplier(s SupplierCommitment, audit Audit, amount int64, reference, effectiveDate string) (SupplierCommitment, Event, error) {
	if !s.valid() || !validAudit(audit) || amount <= 0 || amount > s.Committed-s.Paid+s.RefundReceived || reference == "" || !validDate(effectiveDate) {
		return SupplierCommitment{}, Event{}, ErrInvalid
	}
	s.Paid += amount
	s.Version++
	if !s.valid() {
		return SupplierCommitment{}, Event{}, ErrInvalid
	}
	return s, Event{Audit: audit, SchemaVersion: 1, Type: "SUPPLIER_PAID", TripID: s.TripID, Amount: amount, Reference: reference, EffectiveDate: effectiveDate, Entries: []Entry{{"SUPPLIER_ADVANCES", amount}, {"BANK", -amount}}}, nil
}

// ReceiveSupplierRefund reconoce la recuperación solo cuando está confirmada en banco.
func ReceiveSupplierRefund(s SupplierCommitment, audit Audit, amount int64, reference, effectiveDate string) (SupplierCommitment, Event, error) {
	if !s.valid() || !validAudit(audit) || amount <= 0 || amount > s.RefundAgreed-s.RefundReceived || reference == "" || !validDate(effectiveDate) {
		return SupplierCommitment{}, Event{}, ErrInvalid
	}
	s.RefundReceived += amount
	s.Version++
	return s, Event{Audit: audit, SchemaVersion: 1, Type: "SUPPLIER_REFUND_RECEIVED", TripID: s.TripID, Amount: amount, Reference: reference, EffectiveDate: effectiveDate, Entries: []Entry{{"BANK", amount}, {"SUPPLIER_ADVANCES", -amount}}}, nil
}

// CashPeriod representa caja efectiva en un período, independiente de cuándo se registró el dato.
type CashPeriod struct {
	Opening  int64 `json:"opening"`
	Inflows  int64 `json:"inflows"`
	Outflows int64 `json:"outflows"`
	Closing  int64 `json:"closing"`
}

// ProjectCash reconstruye caja desde partidas verificadas y rechaza entradas duplicadas o desbalanceadas.
// opening corresponde al inicio de from; events debe contener todos los movimientos del período solicitado.
func ProjectCash(opening int64, from, to, tripID string, events []Event) (CashPeriod, error) {
	p := CashPeriod{Opening: opening}
	if opening < -MaxAmount || opening > MaxAmount || !validDate(from) || !validDate(to) || from > to {
		return p, ErrInvalid
	}
	seen := map[string]bool{}
	for _, e := range events {
		if !validAudit(e.Audit) || seen[e.CommandID] {
			return CashPeriod{}, ErrInvalid
		}
		seen[e.CommandID] = true
		var balanced, bank int64
		for _, entry := range e.Entries {
			if entry.Amount < -MaxAmount || entry.Amount > MaxAmount || entry.Account == "" {
				return CashPeriod{}, ErrInvalid
			}
			balanced += entry.Amount
			if entry.Account == "BANK" {
				bank += entry.Amount
			}
		}
		if balanced != 0 {
			return CashPeriod{}, ErrInvalid
		}
		if bank == 0 {
			continue
		}
		if !validDate(e.EffectiveDate) {
			return CashPeriod{}, ErrInvalid
		}
		if (tripID != "" && tripID != e.TripID) || e.EffectiveDate < from || e.EffectiveDate > to {
			continue
		}
		if bank > 0 {
			p.Inflows += bank
		} else {
			p.Outflows -= bank
		}
		if p.Inflows > MaxAmount || p.Outflows > MaxAmount {
			return CashPeriod{}, ErrInvalid
		}
	}
	p.Closing = p.Opening + p.Inflows - p.Outflows
	if p.Closing < -MaxAmount || p.Closing > MaxAmount {
		return CashPeriod{}, ErrInvalid
	}
	return p, nil
}
