package templates

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/signintech/gopdf"

	"ind-hub-api-gox-sls-pri-gh/services/api-program/domain"
)

// BrochureDefault declara la secuencia común inicial de todos los destinos.
// La plantilla concentra contenido y estilo; el compositor solo administra el
// documento, el cursor y los saltos de página.
func BrochureDefault() Template {
	return Template{
		id: BrochureDefaultID,
		build: func(request domain.BudgetRequest, generatedAt time.Time) []Block {
			return []Block{
				Header{
					ProgramName:   request.ProgramName,
					Destination:   request.Destination.Display,
					DepartureCity: request.DepartureCity,
				},
				TripSummary{TotalDays: request.TotalDays, TotalNights: request.TotalNights},
				ServiceList{Names: request.ServiceNames},
				ScenarioTable{Scenarios: request.Scenarios},
				Footer{GeneratedAt: generatedAt},
			}
		},
	}
}

// Header presenta el programa y su ruta principal.
type Header struct {
	ProgramName   string
	Destination   string
	DepartureCity string
}

// Height implementa Block.
func (Header) Height(*Canvas) float64 { return 112 }

// Render implementa Block.
func (h Header) Render(canvas *Canvas) error {
	canvas.PDF.SetFillColor(17, 78, 72)
	canvas.PDF.RectFromUpperLeftWithStyle(canvas.X, canvas.Y, canvas.Width, 104, "F")
	canvas.PDF.SetTextColor(255, 255, 255)

	if err := canvas.setFont(true, 11); err != nil {
		return fmt.Errorf("configurar fuente del encabezado: %w", err)
	}
	if err := canvas.cellAt(canvas.X+18, canvas.Y+15, canvas.Width-36, 18, "PRESUPUESTO DE VIAJE", gopdf.Left); err != nil {
		return fmt.Errorf("dibujar etiqueta del encabezado: %w", err)
	}

	if err := canvas.setFont(true, 22); err != nil {
		return fmt.Errorf("configurar fuente del nombre: %w", err)
	}
	if err := canvas.cellAt(canvas.X+18, canvas.Y+37, canvas.Width-36, 30, truncate(h.ProgramName, 52), gopdf.Left); err != nil {
		return fmt.Errorf("dibujar nombre del programa: %w", err)
	}

	if err := canvas.setFont(false, 11); err != nil {
		return fmt.Errorf("configurar fuente de la ruta: %w", err)
	}
	route := fmt.Sprintf("%s · Salida desde %s", h.Destination, h.DepartureCity)
	if err := canvas.cellAt(canvas.X+18, canvas.Y+74, canvas.Width-36, 18, truncate(route, 76), gopdf.Left); err != nil {
		return fmt.Errorf("dibujar ruta del programa: %w", err)
	}

	canvas.PDF.SetTextColor(31, 41, 55)
	canvas.Advance(h.Height(canvas))
	return nil
}

// TripSummary muestra la duración general del viaje.
type TripSummary struct {
	TotalDays   int
	TotalNights int
}

// Height implementa Block.
func (TripSummary) Height(*Canvas) float64 { return 64 }

// Render implementa Block.
func (s TripSummary) Render(canvas *Canvas) error {
	if err := sectionTitle(canvas, "RESUMEN DEL VIAJE"); err != nil {
		return err
	}
	canvas.PDF.SetFillColor(240, 253, 250)
	canvas.PDF.RectFromUpperLeftWithStyle(canvas.X, canvas.Y+25, canvas.Width, 32, "F")
	if err := canvas.setFont(true, 12); err != nil {
		return fmt.Errorf("configurar fuente del resumen: %w", err)
	}
	text := fmt.Sprintf("%d días  ·  %d noches", s.TotalDays, s.TotalNights)
	if err := canvas.cellAt(canvas.X+12, canvas.Y+29, canvas.Width-24, 24, text, gopdf.Center); err != nil {
		return fmt.Errorf("dibujar resumen del viaje: %w", err)
	}
	canvas.Advance(s.Height(canvas))
	return nil
}

// ServiceList enumera los servicios incluidos sin valorizarlos por separado.
type ServiceList struct {
	Names []string
}

// Height implementa Block.
func (s ServiceList) Height(*Canvas) float64 {
	return 34 + float64(max(1, len(s.Names)))*18
}

// Render implementa Block y permite que una lista extensa continúe en páginas
// nuevas sin cortar una fila.
func (s ServiceList) Render(canvas *Canvas) error {
	if err := sectionTitle(canvas, "SERVICIOS INCLUIDOS"); err != nil {
		return err
	}
	canvas.Advance(27)

	if len(s.Names) == 0 {
		if err := serviceRow(canvas, "Sin servicios informados"); err != nil {
			return err
		}
		canvas.Advance(7)
		return nil
	}

	for _, name := range s.Names {
		canvas.EnsureSpace(24)
		if err := serviceRow(canvas, "• "+truncate(name, 72)); err != nil {
			return err
		}
	}
	canvas.Advance(7)
	return nil
}

func serviceRow(canvas *Canvas, text string) error {
	if err := canvas.setFont(false, 10); err != nil {
		return fmt.Errorf("configurar fuente de servicio: %w", err)
	}
	if err := canvas.cellAt(canvas.X+8, canvas.Y, canvas.Width-16, 18, text, gopdf.Left); err != nil {
		return fmt.Errorf("dibujar servicio: %w", err)
	}
	canvas.Advance(18)
	return nil
}

// ScenarioTable presenta hasta cuatro alternativas de pasajeros y precio.
type ScenarioTable struct {
	Scenarios []domain.BudgetScenario
}

// Height implementa Block.
func (ScenarioTable) Height(*Canvas) float64 { return 154 }

// Render implementa Block.
func (s ScenarioTable) Render(canvas *Canvas) error {
	if err := sectionTitle(canvas, "ESCENARIOS DE PRECIO"); err != nil {
		return err
	}
	canvas.Advance(29)

	labelWidth := 132.0
	columnWidth := (canvas.Width - labelWidth) / float64(len(s.Scenarios))
	rows := []struct {
		label  string
		values func(domain.BudgetScenario) string
	}{
		{label: "Escenario", values: func(_ domain.BudgetScenario) string { return "" }},
		{label: "Pasajeros", values: func(s domain.BudgetScenario) string { return strconv.Itoa(s.TotalPassengers) }},
		{label: "Liberados", values: func(s domain.BudgetScenario) string { return strconv.Itoa(s.FreePassengers) }},
		{label: "Pagantes", values: func(s domain.BudgetScenario) string { return strconv.Itoa(s.PayingPassengers) }},
		{label: "Precio por persona", values: func(s domain.BudgetScenario) string { return "$ " + formatCLP(s.PricePerPassengerCLP) }},
	}

	for rowIndex, row := range rows {
		rowY := canvas.Y + float64(rowIndex)*22
		canvas.PDF.SetFillColor(240, 253, 250)
		canvas.PDF.RectFromUpperLeftWithStyle(canvas.X, rowY, labelWidth, 22, "F")
		if err := canvas.setFont(rowIndex == 0 || rowIndex == len(rows)-1, 9); err != nil {
			return fmt.Errorf("configurar fuente de tabla: %w", err)
		}
		if err := canvas.cellAt(canvas.X+5, rowY, labelWidth-10, 22, row.label, gopdf.Left); err != nil {
			return fmt.Errorf("dibujar etiqueta de tabla: %w", err)
		}

		for scenarioIndex, scenario := range s.Scenarios {
			x := canvas.X + labelWidth + float64(scenarioIndex)*columnWidth
			canvas.PDF.SetStrokeColor(204, 213, 221)
			canvas.PDF.RectFromUpperLeftWithStyle(x, rowY, columnWidth, 22, "D")
			value := row.values(scenario)
			if rowIndex == 0 {
				value = fmt.Sprintf("Opción %d", scenarioIndex+1)
			}
			if err := canvas.cellAt(x+3, rowY, columnWidth-6, 22, value, gopdf.Center); err != nil {
				return fmt.Errorf("dibujar valor de tabla: %w", err)
			}
		}
	}

	canvas.Advance(s.Height(canvas) - 29)
	return nil
}

// Footer cierra el documento con su fecha de generación.
type Footer struct {
	GeneratedAt time.Time
}

// Height implementa Block.
func (Footer) Height(*Canvas) float64 { return 44 }

// Render implementa Block.
func (f Footer) Render(canvas *Canvas) error {
	canvas.PDF.SetStrokeColor(17, 78, 72)
	canvas.PDF.SetLineWidth(1)
	canvas.PDF.Line(canvas.X, canvas.Y+4, canvas.X+canvas.Width, canvas.Y+4)
	if err := canvas.setFont(false, 9); err != nil {
		return fmt.Errorf("configurar fuente del pie: %w", err)
	}
	text := "Presupuesto generado el " + f.GeneratedAt.In(time.UTC).Format("02-01-2006")
	if err := canvas.cellAt(canvas.X, canvas.Y+12, canvas.Width, 18, text, gopdf.Center); err != nil {
		return fmt.Errorf("dibujar pie del documento: %w", err)
	}
	canvas.Advance(f.Height(canvas))
	return nil
}

func sectionTitle(canvas *Canvas, title string) error {
	canvas.PDF.SetTextColor(17, 78, 72)
	if err := canvas.setFont(true, 11); err != nil {
		return fmt.Errorf("configurar fuente de título: %w", err)
	}
	if err := canvas.cellAt(canvas.X, canvas.Y, canvas.Width, 20, title, gopdf.Left); err != nil {
		return fmt.Errorf("dibujar título de sección: %w", err)
	}
	canvas.PDF.SetTextColor(31, 41, 55)
	return nil
}

func formatCLP(amount int64) string {
	digits := strconv.FormatInt(amount, 10)
	groups := make([]string, 0, (len(digits)+2)/3)
	for len(digits) > 3 {
		groups = append([]string{digits[len(digits)-3:]}, groups...)
		digits = digits[:len(digits)-3]
	}
	groups = append([]string{digits}, groups...)
	return strings.Join(groups, ".")
}

func truncate(value string, maxRunes int) string {
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes-1]) + "…"
}
