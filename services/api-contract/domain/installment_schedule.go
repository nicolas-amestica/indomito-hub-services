package domain

import (
	"ind-hub-api-gox-sls-pri-gh/libs/calendar"
)

// UseCurrentTerms aplica las condiciones vigentes solo al crear/editar documentos no aprobados.
func UseCurrentTerms(content *Content) {
	content.Payments.Conditions.RefundPolicyVersion = 2
	// El porcentaje antiguo no representa la política nueva ni debe mostrarse como tal.
	content.Payments.Conditions.CancellationPenaltyPercentage = 0
}

// DueDates genera vencimientos civiles YYYY-MM-DD sin inferir el año ni desbordar febrero.
// Mantiene el día pactado como ancla; en meses más cortos vence el último día del mes.
func (i Installments) DueDates() ([]string, error) {
	return calendar.MonthlyDates(i.StartYear, calendar.MonthNumber(i.StartMonth), i.StartDay, i.Quantity)
}
