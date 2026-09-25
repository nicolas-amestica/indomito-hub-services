package functions

import (
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/signintech/gopdf"
	"ind-hub-api-gox-sls-pri-gh/services/api-contract/domain"
)

const pageWidth = 595.28
const pageHeight = 841.89
const margin = 52.0

//go:embed assets/header.png
var headerPNG []byte

//go:embed assets/footer.png
var footerPNG []byte

//go:embed assets/EBGaramond.ttf
var garamondTTF []byte

type contractPDF struct {
	pdf     *gopdf.GoPdf
	y       float64
	preview bool
}

func ComposePDF(c domain.Content, preview bool) ([]byte, error) {
	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := p.AddTTFFontData("garamond", garamondTTF); err != nil {
		return nil, err
	}
	d := &contractPDF{pdf: p, preview: preview}
	d.addPage()
	d.center("CONTRATO DE PRESTACIÓN DE SERVICIOS TURÍSTICOS", 12, true)
	d.y += 14
	d.paragraph(buildGeneral(c))
	d.y += 8
	for _, clause := range buildClauses(c) {
		d.heading(clause.title)
		d.paragraph(clause.text)
		if clause.number == 16 {
			d.plan(c.Plan, c.Trip)
		}
		if clause.number == 22 {
			d.passengerTable(c.Passengers)
		}
		d.y += 7
	}
	d.addPage()
	d.signatures(c)
	bytes, err := p.GetBytesPdfReturnErr()
	if err != nil {
		return nil, err
	}
	return bytes, nil
}

type clause struct {
	number      int
	title, text string
}

func buildGeneral(c domain.Content) string {
	return fmt.Sprintf("En %s, %s, entre GIRAS INDÓMITO LIMITADA, RUT %s, representada legalmente por %s, según se acredita, en adelante \"El Operador\", por una parte; y por otra, %s, en representación del %s, curso %s, en adelante \"El Pasajero\" o \"Los Representantes\".", fallback(c.Trip.City), displayDate(c.Trip.ContractDate), fallback(c.Payments.BankAccount.HolderDNI), people(c.Representatives), people(c.ClientRepresentatives), fallback(c.Institution.Name), fallback(c.Institution.Course))
}
func buildClauses(c domain.Content) []clause {
	p := c.Payments
	co := p.Conditions
	t := c.Trip
	return []clause{
		{1, "PRIMERO:", fmt.Sprintf("El Operador y el Pasajero han convenido la realización de un programa de viaje con destino a %s, con fecha de salida el %s y retorno el %s, partiendo desde %s, domiciliado en %s, y retornando al mismo lugar de origen. El programa detallado ha sido firmado por los comparecientes y forma parte integrante de este contrato por acuerdo unánime de ambas partes.", fallback(t.Destination), displayDate(t.DepartureDate), displayDate(t.ReturnDate), fallback(t.DeparturePoint), fallback(c.Institution.Address))},
		{2, "SEGUNDO:", "El transporte de los pasajeros se realizará en los medios pactados en el presente contrato, ya sean aéreos, terrestres o marítimos, cuya prestación es de exclusiva responsabilidad del Operador."},
		{3, "TERCERO:", "El alojamiento hotelero de los pasajeros se realizará en los hoteles, habitaciones y regímenes pactados. Sin perjuicio de lo anterior, estos podrán ser modificados garantizando la misma categoría y condiciones de los servicios contratados, asegurando que todos los pasajeros del grupo permanezcan en un mismo establecimiento."},
		{4, "CUARTO:", "El Operador podrá introducir cambios en las rutas y horarios previamente establecidos, siempre que sean acordados con los Representantes del viaje, por las siguientes razones: a) De fuerza mayor, cuando pudieran afectar la seguridad de los pasajeros; b) Aquellas destinadas a mejorar el cumplimiento de los objetivos previstos."},
		{5, "QUINTO:", "El programa deberá cumplirse en los términos estipulados. En todo caso, el representante del Operador (Coordinador) podrá convenir con los Representantes otras actividades que sean consideradas apropiadas por las partes. Estas ampliaciones del programa original serán de exclusivo cargo de los Representantes."},
		{6, "SEXTO:", "La seguridad de los pasajeros es responsabilidad común de ambas partes. Las responsabilidades que se originen por conducta inadecuada de los pasajeros son de carácter individual, según corresponda al o los autores directos."},
		{7, "SÉPTIMO:", "Cualquier daño, desperfecto o deterioro parcial de partes, piezas, instalaciones o accesorios de los vehículos o de los establecimientos comerciales, hoteles, moteles, restaurantes y otros que presten servicios previstos en el programa, ocasionado por hecho o culpa del pasajero o sus acompañantes, será de responsabilidad exclusiva de sus autores, si se pueden identificar, o del grupo en su totalidad si ello no fuera posible. Corresponderá al pasajero o sus acompañantes indemnizar inmediatamente a los afectados. El Operador deslinda toda responsabilidad en dichos eventos, sin perjuicio de cooperar en la búsqueda de soluciones apropiadas y justas."},
		{8, "OCTAVO:", "El Operador no se responsabilizará por daños y perjuicios que pudieran sufrir los pasajeros, sus bienes o personas, cuando la responsabilidad sea de estos mismos. Es deber del o los pasajeros mayores de edad velar por la integridad de los estudiantes, así como también controlar y regular el consumo de bebidas alcohólicas, drogas y/o medicamentos."},
		{9, "NOVENO:", "Todos los gastos que se generen como consecuencia de acciones u omisiones de los pasajeros serán de exclusivo cargo de estos. Asimismo, los gastos que se generen como consecuencia de acciones u omisiones del Operador serán de exclusivo cargo de este último."},
		{10, "DÉCIMO:", "En caso de que el viaje tuviera que acortarse o prolongarse de los términos pactados por razones fuera de control de ambas partes, tales como catástrofes naturales, accidentes, cortes de puentes, caminos obstruidos u otras no imputables al Operador ni al Pasajero, se considerará que no se pudieron cumplir por fuerza mayor. Sin perjuicio de lo anterior, el Operador prestará toda la ayuda necesaria para dar por cumplido el servicio o buscar las compensaciones que correspondan."},
		{11, "DÉCIMO PRIMERO:", "Los participantes del programa quedan cubiertos por el seguro correspondiente a los vehículos de transporte de pasajeros, según la legislación del país de destino."},
		{12, "DÉCIMO SEGUNDO:", fmt.Sprintf("Los programas incluirán exclusivamente lo señalado en el presente contrato y serán confirmados bajo previa reserva con una cuota inicial correspondiente al %d%% del valor del programa con aéreos y %d%% en aquellos que no incluyen aéreos, o la suma de %s por persona en caso de Programas Especiales. Dicho monto será abonado al valor total. El saldo faltante deberá estar cancelado en un plazo no superior a %d días antes de la salida del vuelo o %d días antes de la ocupación de los servicios terrestres.", co.DepositPercentageWithFlight, co.DepositPercentageWithoutFlight, money(co.SpecialProgramDeposit), co.DaysBeforeFlightBalance, co.DaysBeforeTerrestrialBalance)},
		{13, "DÉCIMO TERCERO:", "Todo pasajero que documente o cancele la totalidad del viaje con posterioridad al tiempo estipulado en el contrato estará sujeto a las diferencias de precio que pudieran producirse por variación del tipo de cambio y/o intereses que procedieran."},
		{14, "DÉCIMO CUARTO:", "En caso de modificaciones al programa contratado en fechas posteriores por parte del Pasajero, dichos cambios podrán hacerse efectivos siempre que existan disponibilidades por parte de los prestadores de servicios. Cualquier costo adicional será de exclusiva responsabilidad del Pasajero."},
		{15, "DÉCIMO QUINTO:", "Será de exclusiva responsabilidad del Pasajero cumplir con toda la documentación y requisitos para ingresar al país de destino, como la presentación de documentos vigentes en aeropuertos y aduanas. Los costos adicionales por incumplimiento serán de exclusiva responsabilidad del Pasajero."},
		{16, "DÉCIMO SEXTO:", "El programa contratado incluirá los siguientes servicios:"},
		{17, "DÉCIMO SÉPTIMO:", fmt.Sprintf("La cantidad inicial es de %d pasajeros más %d liberados de pago. Cada pasajero pagante cancela %s, totalizando %s para el grupo. La firma se realiza mediante un abono de %s, quedando un saldo grupal de %s, pagadero en %d cuotas mensuales desde %s. El viaje debe estar pagado como máximo %d días antes de la salida. Si el dólar supera %s, el viaje deberá reprogramarse o pagarse la diferencia. Las transferencias o depósitos se efectuarán a la cuenta corriente %s de %s, RUT %s, %s. Enviar comprobante a %s.", p.TotalPassengers, p.FreePassengers, money(p.PricePerPerson), money(p.TotalGroup), money(p.DownPayment), money(p.GroupBalance), p.Installments.Quantity, fallback(p.Installments.StartMonth), p.DaysBeforePayment, money(p.MaxExchangeRate), fallback(p.BankAccount.AccountNumber), fallback(p.BankAccount.AccountHolder), fallback(p.BankAccount.HolderDNI), fallback(p.BankAccount.Bank), fallback(p.BankAccount.Email))},
		{18, "DÉCIMO OCTAVO:", "En la eventualidad de que no se cumpla con la cantidad de pasajeros acordada en el artículo DÉCIMO SÉPTIMO, se deberá cancelar la cantidad total acordada en el artículo anterior. En caso de no alcanzar el monto, se debe avisar con anterioridad al Operador para buscar una solución que implique el cambio del programa."},
		{19, "DÉCIMO NOVENO:", fmt.Sprintf("Ante cualquier reclamo respecto de los servicios contratados, el Pasajero deberá informar por escrito dentro de %d días. Las dificultades que no puedan solucionarse armoniosamente se someterán a un árbitro arbitrador designado de común acuerdo y, en subsidio, a la justicia ordinaria.", co.ComplaintDeadlineDays)},
		{20, "VIGÉSIMO:", fmt.Sprintf("El presente contrato tendrá vigencia únicamente en las fechas estipuladas. Cualquier cambio deberá avisarse por escrito con %d días de anticipación a la salida.", co.CancellationNoticeDays)},
		{21, "VIGÉSIMO PRIMERO:", fmt.Sprintf("En caso de que el pasajero, los pasajeros o el grupo completo desista del viaje, perderá automáticamente el %d%% del valor total del viaje como compensación por reservas y gastos operacionales.", co.CancellationPenaltyPercentage)},
		{22, "VIGÉSIMO SEGUNDO:", "El Operador habilitará a los Representantes o al Pasajero el acceso a un portal en línea para registrar y actualizar la lista de pasajeros del programa. Es responsabilidad exclusiva del Pasajero y de los Representantes mantener dicha lista completa, veraz y al día, con todos los datos exigidos. La lista registrada es la que se presentará en pasos fronterizos y trámites aduaneros. Cualquier omisión, dato faltante o desactualización y sus consecuencias serán de exclusiva responsabilidad del Pasajero y de los Representantes."},
		{23, "", "En comprobante, previa lectura, firman y ratifican como representantes."}}
}

func (d *contractPDF) addPage() {
	d.pdf.AddPage()
	d.y = 160
	if holder, err := gopdf.ImageHolderByBytes(headerPNG); err == nil {
		_ = d.pdf.ImageByHolder(holder, -8, 0, &gopdf.Rect{W: 612, H: 163})
	}
	if holder, err := gopdf.ImageHolderByBytes(footerPNG); err == nil {
		_ = d.pdf.ImageByHolder(holder, -15, 768, &gopdf.Rect{W: 625, H: 74})
	}
	if d.preview {
		_ = d.pdf.SetFont("garamond", "", 42)
		d.pdf.SetTextColor(225, 225, 225)
		d.pdf.SetX(155)
		d.pdf.SetY(420)
		d.pdf.Rotate(35, 297, 421)
		_ = d.pdf.Cell(nil, "BORRADOR")
		d.pdf.RotateReset()
		d.pdf.SetTextColor(0, 0, 0)
	}
}
func (d *contractPDF) ensure(h float64) {
	if d.y+h > 760 {
		d.addPage()
	}
}
func (d *contractPDF) setFont(bold bool, size int) {
	_ = bold
	_ = d.pdf.SetFont("garamond", "", size)
}
func (d *contractPDF) center(text string, size int, bold bool) {
	d.setFont(bold, size)
	d.ensure(float64(size + 8))
	d.pdf.SetX(margin)
	d.pdf.SetY(d.y)
	_ = d.pdf.CellWithOption(&gopdf.Rect{W: pageWidth - 2*margin, H: 18}, text, gopdf.CellOption{Align: gopdf.Center})
	d.y += 20
}
func (d *contractPDF) heading(text string) {
	if text == "" {
		return
	}
	d.ensure(18)
	d.setFont(true, 12)
	d.pdf.SetX(margin)
	d.pdf.SetY(d.y)
	_ = d.pdf.Cell(nil, text)
	d.y += 16
}
func (d *contractPDF) paragraph(text string) {
	d.setFont(false, 12)
	for _, line := range wrap(text, 76) {
		d.ensure(16)
		d.pdf.SetX(margin)
		d.pdf.SetY(d.y)
		_ = d.pdf.Cell(nil, line)
		d.y += 15
	}
}
func (d *contractPDF) plan(p domain.Plan, t domain.Trip) {
	d.y += 4
	d.heading(fallback(p.Name))
	d.paragraph(fmt.Sprintf("%d días / %d noches de estadía", t.Days, t.Nights))
	d.heading("Servicios incluidos:")
	for _, s := range p.ServicesIncluded {
		d.paragraph("• " + s.Description)
	}
}
func (d *contractPDF) passengerTable(rows []domain.Passenger) {
	d.y += 4
	d.heading("NÓMINA DE PASAJEROS")
	widths := []float64{94, 94, 72, 76, 78, 77}
	headers := []string{"Nombres", "Apellidos", "RUT", "Fecha nac.", "Nacionalidad", "Sexo"}
	d.tableRow(headers, widths, true, false)
	for index, r := range rows {
		d.tableRow([]string{r.Names, r.LastNames, r.DNI, displayDate(r.BirthDate), r.Nationality, sexLabel(r.Sex)}, widths, false, index%2 == 1)
	}
}
func (d *contractPDF) tableRow(values []string, widths []float64, header bool, alternate bool) {
	const height = 34.0
	d.ensure(height + 2)
	d.setFont(header, 12)
	x := margin
	for i, v := range values {
		if header {
			d.pdf.SetFillColor(32, 55, 72)
		} else if alternate {
			d.pdf.SetFillColor(240, 245, 247)
		} else {
			d.pdf.SetFillColor(255, 255, 255)
		}
		d.pdf.RectFromUpperLeftWithStyle(x, d.y, widths[i], height, "F")
		d.pdf.SetTextColor(0, 0, 0)
		if header {
			d.pdf.SetTextColor(255, 255, 255)
		}
		for lineIndex, line := range tableCellLines(v, widths[i]) {
			d.pdf.SetX(x + 4)
			d.pdf.SetY(d.y + 5 + float64(lineIndex*13))
			_ = d.pdf.CellWithOption(&gopdf.Rect{W: widths[i] - 8, H: 13}, line, gopdf.CellOption{Align: gopdf.Left})
		}
		d.pdf.SetStrokeColor(198, 208, 214)
		d.pdf.RectFromUpperLeftWithStyle(x, d.y, widths[i], height, "D")
		x += widths[i]
	}
	d.pdf.SetTextColor(0, 0, 0)
	d.y += height
}
func (d *contractPDF) signatures(c domain.Content) {
	d.center("FIRMAS", 11, true)
	d.y += 45
	all := append(append([]domain.Person{}, c.Representatives...), c.ClientRepresentatives...)
	for i, p := range all {
		d.ensure(80)
		x := margin
		if i%2 == 1 {
			x = 315
		}
		d.pdf.SetX(x)
		d.pdf.SetY(d.y)
		d.pdf.Line(x, d.y, x+200, d.y)
		d.setFont(true, 12)
		d.pdf.SetY(d.y - 16)
		d.pdf.SetX(x)
		_ = d.pdf.Cell(nil, fallback(p.Name))
		d.setFont(false, 12)
		d.pdf.SetY(d.y - 29)
		d.pdf.SetX(x)
		_ = d.pdf.Cell(nil, "RUT "+fallback(p.DNI))
		if i%2 == 1 {
			d.y += 90
		}
	}
}
func people(rows []domain.Person) string {
	parts := make([]string, 0, len(rows))
	for _, p := range rows {
		parts = append(parts, fmt.Sprintf("%s, cédula nacional de identidad número %s", fallback(p.Name), fallback(p.DNI)))
	}
	if len(parts) == 0 {
		return ":::SIN REPRESENTANTE:::"
	}
	return strings.Join(parts, ", ")
}
func fallback(v string) string {
	if strings.TrimSpace(v) == "" {
		return ":::SIN INFORMACIÓN:::"
	}
	return strings.TrimSpace(v)
}
func money(v int64) string { return fmt.Sprintf("$%d", v) }
func displayDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback(value)
	}
	if date, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return date.UTC().Format("02/01/2006")
	}
	return fallback(value)
}
func sexLabel(v string) string {
	switch v {
	case "FEMALE":
		return "Femenino"
	case "MALE":
		return "Masculino"
	case "OTHER":
		return "Otro"
	case "NOT_SPECIFIED":
		return "No indica"
	default:
		return fallback(v)
	}
}
func tableCellLines(value string, width float64) []string {
	max := int(width / 6.2)
	if max < 5 {
		max = 5
	}
	lines := wrap(value, max)
	if len(lines) <= 2 {
		return lines
	}
	last := []rune(lines[1])
	if len(last) >= max {
		last = last[:max-1]
	}
	lines[1] = string(last) + "…"
	return lines[:2]
}
func wrap(text string, max int) []string {
	words := strings.Fields(text)
	lines := []string{}
	line := ""
	for _, word := range words {
		if len([]rune(line))+1+len([]rune(word)) > max {
			lines = append(lines, line)
			line = word
		} else if line == "" {
			line = word
		} else {
			line += " " + word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
