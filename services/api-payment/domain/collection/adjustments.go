package collection

// Discount aplica puntos base sobre saldos seleccionados (10000 = 100%). Redondea al peso inferior.
// Los beneficiarios se fijan al aprobar el comando; no se incorporan altas futuras automáticamente.
func Discount(a Account, audit Audit, ids []string, basisPoints int64) (Change, error) {
	if !a.Active || len(ids) == 0 || basisPoints <= 0 || basisPoints > 10000 {
		return Change{}, ErrInvalid
	}
	if a.OpenAttemptID != "" {
		return Change{}, ErrConflict
	}
	c, err := begin(a, audit, "DISCOUNT_APPROVED")
	if err != nil {
		return Change{}, err
	}
	selected := map[string]bool{}
	for _, id := range ids {
		if selected[id] {
			return Change{}, ErrInvalid
		}
		selected[id] = true
	}
	for index, i := range a.Installments {
		if !selected[i.ID] {
			continue
		}
		delete(selected, i.ID)
		basis := i.Outstanding()
		if basis <= 0 {
			return Change{}, ErrInvalid
		}
		amount := basis * basisPoints / 10000
		c.Account.Installments[index].Discount += amount
		c.Event.Basis += basis
		c.Event.Amount += amount
	}
	if len(selected) > 0 || c.Event.Amount == 0 {
		return Change{}, ErrInvalid
	}
	c.Event.BasisPoints = basisPoints
	return finish(c)
}

// AllocateUnappliedFunds mueve dinero ya recibido desde revisión a cuotas completas.
// No registra un segundo ingreso y nunca aplica parcialmente una cuota.
func AllocateUnappliedFunds(a Account, audit Audit, ids []string) (Change, error) {
	if !a.Active || len(ids) == 0 || a.UnappliedReceived <= 0 || a.UnappliedRefundApproved != 0 || a.ReviewedUnapplied != 0 || a.Refunded != 0 {
		return Change{}, ErrConflict
	}
	c, err := begin(a, audit, "UNAPPLIED_FUNDS_ALLOCATED")
	if err != nil {
		return Change{}, err
	}
	selected := map[string]bool{}
	for _, id := range ids {
		if selected[id] {
			return Change{}, ErrInvalid
		}
		selected[id] = true
	}
	for index, installment := range a.Installments {
		if !selected[installment.ID] {
			continue
		}
		delete(selected, installment.ID)
		outstanding := installment.Outstanding()
		if outstanding <= 0 {
			return Change{}, ErrInvalid
		}
		c.Event.Amount += outstanding
		c.Account.Installments[index].Paid += outstanding
	}
	if len(selected) > 0 || c.Event.Amount <= 0 || c.Event.Amount > a.UnappliedReceived {
		return Change{}, ErrInvalid
	}
	c.Account.UnappliedReceived -= c.Event.Amount
	if c.Account.UnappliedReceived == 0 {
		c.Account.ReviewAttemptID = ""
	}
	c.Event.Entries = []Entry{{"UNAPPLIED_FUNDS", c.Event.Amount}, {"CUSTOMER_FUNDS", -c.Event.Amount}}
	return finish(c)
}

// Withdraw cancela únicamente deudas futuras de esta participación y retiene el bloqueo de cobros abiertos.
// No aprueba automáticamente devoluciones ni cambia las cuotas de otros pasajeros.
func Withdraw(a Account, audit Audit, annexID string) (Change, error) {
	if !a.Active || annexID == "" {
		return Change{}, ErrInvalid
	}
	c, err := begin(a, audit, "PARTICIPANT_WITHDRAWN")
	if err != nil {
		return Change{}, err
	}
	c.Account.Active = false
	c.Event.Reference = annexID
	for index, i := range a.Installments {
		amount := i.Outstanding()
		c.Account.Installments[index].Cancelled += amount
		c.Event.Amount += amount
	}
	return finish(c)
}

// ApproveWithdrawalRefund fija o amplía el total reembolsable de cuotas pagadas, excluyendo el abono.
// Una reducción tras aprobación requiere reverso explícito, nunca una sobrescritura silenciosa.
func ApproveWithdrawalRefund(a Account, audit Audit, basisPoints int64) (Change, error) {
	if a.Active || basisPoints < 0 || basisPoints > 10000 {
		return Change{}, ErrInvalid
	}
	c, err := begin(a, audit, "WITHDRAWAL_REFUND_APPROVED")
	if err != nil {
		return Change{}, err
	}
	for _, i := range a.Installments {
		c.Event.Basis += i.Paid
	}
	target := c.Event.Basis * basisPoints / 10000
	if target < a.WithdrawalRefundApproved {
		return Change{}, ErrConflict
	}
	c.Event.Amount = target - a.WithdrawalRefundApproved
	c.Event.BasisPoints = basisPoints
	c.Account.WithdrawalRefundApproved = target
	if c.Event.Amount > 0 {
		c.Event.Entries = []Entry{{"CUSTOMER_FUNDS", c.Event.Amount}, {"REFUND_PAYABLE", -c.Event.Amount}}
	}
	return finish(c)
}

// ApproveUnappliedRefund permite devolver excesos sin aplicar penalidades de baja.
func ApproveUnappliedRefund(a Account, audit Audit, amount int64) (Change, error) {
	if amount <= 0 || amount > a.UnappliedReceived-a.UnappliedRefundApproved {
		return Change{}, ErrInvalid
	}
	c, err := begin(a, audit, "UNAPPLIED_REFUND_APPROVED")
	if err != nil {
		return Change{}, err
	}
	c.Account.UnappliedRefundApproved += amount
	c.Event.Amount = amount
	c.Event.Entries = []Entry{{"UNAPPLIED_FUNDS", amount}, {"REFUND_PAYABLE", -amount}}
	return finish(c)
}

// ConfirmRefund registra exclusivamente una salida bancaria verificada, incluso parcial.
func ConfirmRefund(a Account, audit Audit, amount int64, bankReference, effectiveDate string) (Change, error) {
	if amount <= 0 || amount > a.WithdrawalRefundApproved+a.UnappliedRefundApproved-a.Refunded || bankReference == "" || !validDate(effectiveDate) {
		return Change{}, ErrInvalid
	}
	c, err := begin(a, audit, "REFUND_PAID")
	if err != nil {
		return Change{}, err
	}
	c.Account.Refunded += amount
	c.Event.Amount = amount
	c.Event.Reference = bankReference
	c.Event.EffectiveDate = effectiveDate
	c.Event.Entries = []Entry{{"REFUND_PAYABLE", amount}, {"BANK", -amount}}
	return finish(c)
}
