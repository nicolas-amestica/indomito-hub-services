// Package calendar define calendarios civiles compartidos, independientes de zonas horarias.
package calendar

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// MonthNumber admite el valor numérico del formulario y nombres históricos en español.
func MonthNumber(value string) int {
	if month, err := strconv.Atoi(value); err == nil && month >= 1 && month <= 12 {
		return month
	}
	for i, name := range []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"} {
		if strings.EqualFold(strings.TrimSpace(value), name) {
			return i + 1
		}
	}
	return 0
}

// MonthlyDates conserva el día ancla y usa el último día solo en meses más cortos.
func MonthlyDates(year, month, day, quantity int) ([]string, error) {
	if year < 2000 || year > 2200 || month < 1 || month > 12 || day < 1 || day > 31 || quantity < 1 || quantity > 120 {
		return nil, errors.New("indique mes, ano, dia de inicio y cantidad de cuotas validos")
	}
	if day > time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day() {
		return nil, errors.New("el dia de inicio no existe en el mes y ano seleccionados")
	}
	first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	dates := make([]string, quantity)
	for n := range quantity {
		current := first.AddDate(0, n, 0)
		effectiveDay := min(day, current.AddDate(0, 1, -1).Day())
		dates[n] = time.Date(current.Year(), current.Month(), effectiveDay, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
	}
	return dates, nil
}
