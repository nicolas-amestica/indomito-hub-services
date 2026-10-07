package collection

// ResolveRefundedReview cierra la revisión solo cuando todos los fondos no aplicados
// fueron aprobados y todas las devoluciones comprometidas constan efectivamente pagadas.
// No borra ingresos, no genera caja ni infiere la categoría de una devolución parcial.
func ResolveRefundedReview(a Account, audit Audit) (Change, error) {
	if !a.RequiresPaymentReview() || a.OpenAttemptID != "" || a.UnappliedReceived == 0 || a.UnappliedRefundApproved != a.UnappliedReceived || a.Refunded != a.UnappliedRefundApproved+a.WithdrawalRefundApproved {
		return Change{}, ErrConflict
	}
	c, err := begin(a, audit, "PAYMENT_REVIEW_RESOLVED_REFUNDED")
	if err != nil {
		return Change{}, err
	}
	c.Account.ReviewedUnapplied = a.UnappliedReceived
	c.Account.ReviewAttemptID = ""
	c.Event.Amount = a.UnappliedReceived - a.ReviewedUnapplied
	return finish(c)
}

// RecordManualInstallment aplica únicamente la primera cuota completa a un ingreso
// bancario verificado. No toma el importe del operador ni comparte reservas Khipu abiertas.
func RecordManualInstallment(a Account, audit Audit, reference, date string) (Change, error) {
	if !a.Active || a.Free || reference == "" || !validDate(date) {
		return Change{}, ErrInvalid
	}
	if a.OpenAttemptID != "" || a.RequiresPaymentReview() {
		return Change{}, ErrConflict
	}
	c, err := begin(a, audit, "PAYMENT_RECEIVED")
	if err != nil {
		return Change{}, err
	}
	for index, i := range a.Installments {
		if i.Outstanding() <= 0 {
			continue
		}
		amount := i.Outstanding()
		c.Account.Installments[index].Paid += amount
		c.Event.Amount = amount
		c.Event.Reference = reference
		c.Event.EffectiveDate = date
		c.Event.Entries = []Entry{{"BANK", amount}, {"CUSTOMER_FUNDS", -amount}}
		return finish(c)
	}
	return Change{}, ErrInvalid
}
