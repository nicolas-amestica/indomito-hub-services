package collection

// AgreedInstallment contiene solamente condiciones pactadas, nunca dinero recibido.
type AgreedInstallment struct {
	ID      string `json:"id"`
	DueDate string `json:"dueDate"`
	Amount  int64  `json:"amount"`
}

// Admission abre una participación nueva mediante un anexo. Los importes y fechas
// son explícitos: no se redistribuye deuda del grupo ni se copian cuentas anteriores.
// La capa persistente debe verificar identidad única, anexo aprobado y nómina abierta.
type Admission struct {
	AccountID     string              `json:"accountId"`
	ParticipantID string              `json:"participantId"`
	TripID        string              `json:"tripId"`
	AnnexID       string              `json:"annexId"`
	Free          bool                `json:"free"`
	DepositAgreed int64               `json:"depositAgreed"`
	Installments  []AgreedInstallment `json:"installments"`
}

// Admit crea una cuenta sin cobros, descuentos, devoluciones ni intentos heredados.
// El evento no genera caja: aprobar condiciones no demuestra un ingreso bancario.
func Admit(input Admission, audit Audit) (Change, error) {
	if input.AnnexID == "" || audit.CommandID == "" || audit.Actor == "" || audit.Reason == "" || audit.RecordedAt.IsZero() || input.DepositAgreed < 0 || input.DepositAgreed > MaxAmount || len(input.Installments) > 120 {
		return Change{}, ErrInvalid
	}
	if input.Free && (input.DepositAgreed != 0 || len(input.Installments) != 0) {
		return Change{}, ErrInvalid
	}
	a := Account{ID: input.AccountID, ParticipantID: input.ParticipantID, TripID: input.TripID, Version: 1, Active: true, Free: input.Free, DepositAgreed: input.DepositAgreed, Installments: []Installment{}}
	total := input.DepositAgreed
	previous := ""
	for _, installment := range input.Installments {
		if installment.Amount <= 0 || installment.Amount > MaxAmount-total || installment.DueDate <= previous {
			return Change{}, ErrInvalid
		}
		total += installment.Amount
		previous = installment.DueDate
		a.Installments = append(a.Installments, Installment{ID: installment.ID, DueDate: installment.DueDate, Original: installment.Amount})
	}
	if !input.Free && total == 0 {
		return Change{}, ErrInvalid
	}
	return finish(Change{Account: a, Event: Event{Audit: audit, SchemaVersion: 1, Type: "PARTICIPANT_ADMITTED", AccountID: a.ID, TripID: a.TripID, Reference: input.AnnexID, Amount: total}})
}
