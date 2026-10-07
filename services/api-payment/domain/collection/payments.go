package collection

import "net/mail"

// OpenAttempt bloquea la cuenta antes de crear el cobro externo; no se libera por TTL.
func OpenAttempt(a Account, audit Audit, id, email string) (Change, Attempt, error) {
	c, err := begin(a, audit, "PAYMENT_ATTEMPT_OPENED")
	address, emailErr := mail.ParseAddress(email)
	if err != nil || id == "" || emailErr != nil || address.Address != email || len(email) > 254 || !a.Active || a.Free {
		return Change{}, Attempt{}, ErrInvalid
	}
	if a.OpenAttemptID != "" || a.RequiresPaymentReview() {
		return Change{}, Attempt{}, ErrConflict
	}
	for _, i := range a.Installments {
		if i.Outstanding() > 0 {
			attempt := Attempt{ID: id, AccountID: a.ID, InstallmentID: i.ID, Amount: i.Outstanding(), Email: email, Status: "CREATING"}
			c.Account.OpenAttemptID = id
			c.Event.Amount = attempt.Amount
			c.Event.Reference = id
			return c, attempt, nil
		}
	}
	return Change{}, Attempt{}, ErrInvalid
}

// ResolveUnpaid exige evidencia autoritativa de no pago; nunca se llama por cierre de navegador o timeout.
func ResolveUnpaid(a Account, audit Audit, attempt Attempt, providerReference string) (Change, error) {
	if a.OpenAttemptID != attempt.ID || attempt.ID == "" || attempt.AccountID != a.ID || providerReference == "" || attempt.Status != "UNPAID_FINAL" {
		return Change{}, ErrConflict
	}
	c, err := begin(a, audit, "PAYMENT_ATTEMPT_RESOLVED_UNPAID")
	if err != nil {
		return Change{}, err
	}
	c.Account.OpenAttemptID = ""
	c.Event.Reference = providerReference
	c.Event.AttemptID = attempt.ID
	return finish(c)
}

// ReverseProviderPayment revierte exactamente el efecto del ingreso original.
// Si fondos en revisión ya fueron comprometidos o devueltos, exige resolución manual.
func ReverseProviderPayment(a Account, audit Audit, attempt Attempt, originalType string, amount int64, reference, effectiveDate string) (Change, error) {
	if attempt.ID == "" || attempt.AccountID != a.ID || amount <= 0 || reference == "" || !validDate(effectiveDate) {
		return Change{}, ErrInvalid
	}
	c, err := begin(a, audit, "PAYMENT_REVERSED_BY_PROVIDER")
	if err != nil {
		return Change{}, err
	}
	switch originalType {
	case "PAYMENT_RECEIVED":
		found := false
		for index, installment := range a.Installments {
			if installment.ID == attempt.InstallmentID && installment.Paid >= amount {
				c.Account.Installments[index].Paid -= amount
				found = true
				break
			}
		}
		if !found {
			return Change{}, ErrConflict
		}
	case "PAYMENT_REQUIRES_REVIEW":
		if a.UnappliedReceived < amount || a.UnappliedRefundApproved > 0 || a.Refunded > 0 {
			return Change{}, ErrConflict
		}
		c.Account.UnappliedReceived -= amount
		if c.Account.UnappliedReceived == c.Account.ReviewedUnapplied {
			c.Account.ReviewAttemptID = ""
		}
	default:
		return Change{}, ErrInvalid
	}
	c.Event.Amount = amount
	c.Event.Reference = reference
	c.Event.AttemptID = attempt.ID
	c.Event.EffectiveDate = effectiveDate
	c.Event.Entries = []Entry{{"CUSTOMER_FUNDS", amount}, {"PROVIDER_RECEIVABLE", -amount}}
	if originalType == "PAYMENT_REQUIRES_REVIEW" {
		c.Event.Entries[0].Account = "UNAPPLIED_FUNDS"
	}
	return finish(c)
}

// ConfirmPayment registra TODO ingreso verificado. La persistencia deduplica la referencia del proveedor.
// Una confirmación tardía o discrepante queda como fondos no aplicados, sin perder el dinero recibido.
func ConfirmPayment(a Account, audit Audit, attempt Attempt, amount int64, reference, effectiveDate string, manual bool) (Change, error) {
	if attempt.ID == "" || attempt.AccountID != a.ID || amount <= 0 || amount > MaxAmount || reference == "" || !validDate(effectiveDate) {
		return Change{}, ErrInvalid
	}
	c, err := begin(a, audit, "PAYMENT_RECEIVED")
	if err != nil {
		return Change{}, err
	}
	applied := false
	if a.Active && a.OpenAttemptID == attempt.ID && amount == attempt.Amount {
		for index, i := range a.Installments {
			if i.ID == attempt.InstallmentID && i.Outstanding() == amount {
				c.Account.Installments[index].Paid += amount
				applied = true
				break
			}
		}
	}
	if manual && !applied {
		return Change{}, ErrConflict
	}
	credit := "CUSTOMER_FUNDS"
	if !applied {
		c.Account.UnappliedReceived += amount
		if c.Account.ReviewAttemptID == "" {
			c.Account.ReviewAttemptID = attempt.ID
		}
		credit = "UNAPPLIED_FUNDS"
		c.Event.Type = "PAYMENT_REQUIRES_REVIEW"
	}
	if a.OpenAttemptID == attempt.ID {
		c.Account.OpenAttemptID = ""
	}
	debit := "PROVIDER_RECEIVABLE"
	if manual {
		debit = "BANK"
	}
	c.Event.Amount = amount
	c.Event.Reference = reference
	c.Event.AttemptID = attempt.ID
	c.Event.EffectiveDate = effectiveDate
	c.Event.Entries = []Entry{{debit, amount}, {credit, -amount}}
	return finish(c)
}

// RecordDeposit registra una asignación de abono recibido, no el abono pactado.
// reference identifica el ingreso bancario y su asignación para evitar duplicar caja.
func RecordDeposit(a Account, audit Audit, amount int64, reference, effectiveDate string) (Change, error) {
	if !a.Active || a.Free || amount <= 0 || amount > a.DepositAgreed-a.DepositReceived || reference == "" || !validDate(effectiveDate) {
		return Change{}, ErrInvalid
	}
	c, err := begin(a, audit, "DEPOSIT_RECEIVED")
	if err != nil {
		return Change{}, err
	}
	c.Account.DepositReceived += amount
	c.Event.Amount = amount
	c.Event.Reference = reference
	c.Event.EffectiveDate = effectiveDate
	c.Event.Entries = []Entry{{"BANK", amount}, {"CUSTOMER_FUNDS", -amount}}
	return finish(c)
}
