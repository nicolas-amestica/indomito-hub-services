package domain

import (
	"errors"
	"fmt"
	"ind-hub-api-gox-sls-pri-gh/libs/calendar"
	"time"
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

// ValidateBeforeDeparture impide pactar cuotas posteriores al plazo maximo de pago del viaje.
func (i Installments) ValidateBeforeDeparture(departureDate string, daysBeforeDeparture int) error {
	dates, err := i.DueDates()
	if err != nil {
		return err
	}
	if daysBeforeDeparture < 0 || daysBeforeDeparture > 365 {
		return errors.New("los dias de anticipacion del pago deben estar entre 0 y 365")
	}
	if departureDate == "" {
		return nil
	}
	departure, err := time.Parse(time.RFC3339Nano, departureDate)
	if err != nil {
		return errors.New("la fecha de salida no es valida")
	}
	limit := departure.AddDate(0, 0, -daysBeforeDeparture).Format(time.DateOnly)
	if dates[len(dates)-1] > limit {
		return fmt.Errorf("la ultima cuota debe vencer a mas tardar el %s, %d dias antes de la salida", limit, daysBeforeDeparture)
	}
	return nil
}
