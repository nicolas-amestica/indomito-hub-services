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
const margin = 50.0

// Tamaños de fuente — replican la jerarquía visual del legacy (pdf-lib).
const (
	fontSizeTitle           = 14
	fontSizeBody            = 10
	fontSizeClauseTitle     = 11 // Solo para el número de cláusula, ej. "PRIMERO:"
	fontSizeServiceTitle    = 11
	fontSizeService         = 9
	fontSizeSignature       = 10
	fontSizeSignatureDetail = 9
)

// Interlineado y espaciado — tomados del legacy.
const (
	lineHeightBody    = 15.0
	lineHeightService = 12.0
	clauseSpacing     = 15.0
	generalPostGap    = 20.0
)

//go:embed assets/header.png
var headerPNG []byte

//go:embed assets/footer.png
var footerPNG []byte

//go:embed assets/EBGaramond.ttf
var garamondTTF []byte

//go:embed assets/EBGaramond-Bold.ttf
var garamondBoldTTF []byte

type contractPDF struct {
	pdf     *gopdf.GoPdf
	y       float64
	preview bool
}

// contentWidth devuelve el ancho disponible para contenido.
func contentWidth() float64 {
	return pageWidth - 2*margin
}

// ComposePDF genera los bytes de un contrato PDF a partir del contenido.
func ComposePDF(c domain.Content, preview bool) ([]byte, error) {
	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})

	if err := p.AddTTFFontData("garamond", garamondTTF); err != nil {
		return nil, fmt.Errorf("no se pudo cargar la fuente regular: %w", err)
	}
	if err := p.AddTTFFontData("garamond-bold", garamondBoldTTF); err != nil {
		return nil, fmt.Errorf("no se pudo cargar la fuente bold: %w", err)
	}

	d := &contractPDF{pdf: p, preview: preview}
	d.addPage()

	// Título centrado en negrita (tamaño 14 como el legacy).
	d.center("CONTRATO DE PRESTACIÓN DE SERVICIOS TURÍSTICOS", fontSizeTitle, true)
	d.y += 10

	// Datos generales — justificados.
	d.justifiedParagraph(buildGeneral(c), fontSizeBody)
	d.y += generalPostGap

	// Cláusulas.
	for _, clause := range buildClauses(c) {
		// Título de la cláusula en negrita (tamaño 11).
		d.heading(clause.title, fontSizeClauseTitle)
		// Cuerpo justificado (tamaño 10).
		d.justifiedParagraph(clause.text, fontSizeBody)

		// Detalle del plan (cláusula 16).
		if clause.number == 16 {
			d.plan(c.Plan, c.Trip)
		}
		// Tabla de pasajeros (cláusula 22).
		if clause.number == 22 {
			d.passengerTable(c.Passengers)
		}

		d.y += clauseSpacing
	}

	// Página de firmas.
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
		{1, "PRIMERO:", fmt.Sprintf("El Operador y el Pasajero han convenido la realización de un programa de viaje con destino a %s, con fecha de salida el %s y retorno el %s, partiendo desde %s, domiciliado en %s, y retornando al mismo lugar de origen. El programa detallado ha sido firmado por los comparecientes y forma parte integrante de este contrato por acuerdo unánime de ambas partes.", fallback(t.Destination), displayDateFull(t.DepartureDate), displayDateFull(t.ReturnDate), fallback(t.DeparturePoint), fallback(c.Institution.Address))},
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

// ─── Métodos de dibujo ───────────────────────────────────────────────────────

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

// ensure verifica que quede espacio suficiente antes del footer. Si no, agrega una nueva página.
func (d *contractPDF) ensure(h float64) {
	if d.y+h > 760 {
		d.addPage()
	}
}

// setFont alterna entre la fuente regular y bold.
func (d *contractPDF) setFont(bold bool, size int) {
	if bold {
		_ = d.pdf.SetFont("garamond-bold", "", size)
	} else {
		_ = d.pdf.SetFont("garamond", "", size)
	}
}

// center dibuja texto centrado horizontalmente.
func (d *contractPDF) center(text string, size int, bold bool) {
	d.setFont(bold, size)
	d.ensure(float64(size + 8))
	w, _ := d.pdf.MeasureTextWidth(text)
	x := (pageWidth - w) / 2
	d.pdf.SetX(x)
	d.pdf.SetY(d.y)
	_ = d.pdf.Cell(nil, text)
	d.y += float64(size) + 6
}

// heading dibuja un título de cláusula en negrita.
func (d *contractPDF) heading(text string, size int) {
	if text == "" {
		return
	}
	d.ensure(float64(size) + 8)
	d.setFont(true, size)
	d.pdf.SetX(margin)
	d.pdf.SetY(d.y)
	_ = d.pdf.Cell(nil, text)
	d.y += float64(size) + 5
}

// justifiedParagraph dibuja un párrafo con texto justificado, midiendo el ancho
// real de cada palabra con la fuente activa.
func (d *contractPDF) justifiedParagraph(text string, size int) {
	d.setFont(false, size)
	cw := contentWidth()
	lines := d.wrapByWidth(text, cw, false, size)

	for i, line := range lines {
		d.ensure(lineHeightBody)
		isLast := i == len(lines)-1
		d.drawJustifiedLine(line, margin, d.y, cw, isLast, false, size)
		d.y += lineHeightBody
	}
}

// drawJustifiedLine dibuja una línea de texto justificada distribuyendo el
// espacio sobrante entre las palabras. La última línea de un párrafo no se
// justifica (queda alineada a la izquierda, como el legacy).
func (d *contractPDF) drawJustifiedLine(line string, x, y, maxWidth float64, isLastLine bool, bold bool, size int) {
	d.setFont(bold, size)
	words := strings.Fields(line)

	if isLastLine || len(words) <= 1 {
		d.pdf.SetX(x)
		d.pdf.SetY(y)
		_ = d.pdf.Cell(nil, strings.Join(words, " "))
		return
	}

	// Medir el ancho natural de las palabras (sin espacios).
	var wordsWidth float64
	for _, word := range words {
		w, _ := d.pdf.MeasureTextWidth(word)
		wordsWidth += w
	}

	// Medir el ancho de un espacio normal.
	spaceW, _ := d.pdf.MeasureTextWidth(" ")
	naturalWidth := wordsWidth + float64(len(words)-1)*spaceW
	extraPerGap := 0.0
	if len(words) > 1 && maxWidth > naturalWidth {
		extraPerGap = (maxWidth - naturalWidth) / float64(len(words)-1)
	}

	currentX := x
	for i, word := range words {
		d.pdf.SetX(currentX)
		d.pdf.SetY(y)
		_ = d.pdf.Cell(nil, word)
		if i < len(words)-1 {
			w, _ := d.pdf.MeasureTextWidth(word)
			currentX += w + spaceW + extraPerGap
		}
	}
}

// wrapByWidth divide el texto en líneas que caben en maxWidth, midiendo el
// ancho real con la fuente activa. Reemplaza la función wrap() que cortaba a
// 76 caracteres fijos.
func (d *contractPDF) wrapByWidth(text string, maxWidth float64, bold bool, size int) []string {
	d.setFont(bold, size)
	words := strings.Fields(text)
	var lines []string
	var line string

	for _, word := range words {
		test := line
		if test != "" {
			test += " "
		}
		test += word
		w, _ := d.pdf.MeasureTextWidth(test)
		if w > maxWidth && line != "" {
			lines = append(lines, line)
			line = word
		} else {
			line = test
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// ─── Plan y servicios ────────────────────────────────────────────────────────

func (d *contractPDF) plan(p domain.Plan, t domain.Trip) {
	d.y += 10

	// Nombre del plan.
	d.heading(fallback(p.Name), fontSizeServiceTitle)
	d.setFont(false, fontSizeBody)
	d.ensure(lineHeightBody)
	d.pdf.SetX(margin)
	d.pdf.SetY(d.y)
	_ = d.pdf.Cell(nil, fmt.Sprintf("%d días / %d noches de estadía", t.Days, t.Nights))
	d.y += 20

	// Servicios incluidos.
	d.heading("Servicios incluidos:", fontSizeBody)
	for _, s := range p.ServicesIncluded {
		d.serviceItem("• " + s.Description)
	}
}

// serviceItem dibuja un servicio con indentación y justificado.
func (d *contractPDF) serviceItem(text string) {
	indent := 20.0
	d.setFont(false, fontSizeService)
	availWidth := contentWidth() - indent
	lines := d.wrapByWidth(text, availWidth, false, fontSizeService)

	for i, line := range lines {
		d.ensure(lineHeightService)
		isLast := i == len(lines)-1
		d.drawJustifiedLine(line, margin+indent, d.y, availWidth, isLast, false, fontSizeService)
		d.y += lineHeightService
	}
}

// ─── Tabla de pasajeros ──────────────────────────────────────────────────────

func (d *contractPDF) passengerTable(rows []domain.Passenger) {
	d.y += 10
	d.heading("NÓMINA DE PASAJEROS", fontSizeBody)
	d.y += 4
	widths := []float64{94, 94, 72, 76, 78, 77}
	headers := []string{"Nombres", "Apellidos", "RUT", "Fecha nac.", "Nacionalidad", "Sexo"}
	d.tableRow(headers, widths, true)
	for _, r := range rows {
		d.tableRow([]string{r.Names, r.LastNames, r.DNI, displayDateShort(r.BirthDate), r.Nationality, sexLabel(r.Sex)}, widths, false)
	}
}

// tableRow dibuja una fila de tabla sin colores de fondo — solo bordes negros.
func (d *contractPDF) tableRow(values []string, widths []float64, header bool) {
	const height = 22.0
	d.ensure(height + 2)

	fontSize := fontSizeService
	if header {
		fontSize = fontSizeBody
	}
	d.setFont(header, fontSize)

	x := margin
	for i, v := range values {
		// Fondo blanco siempre (sin color).
		d.pdf.SetFillColor(255, 255, 255)
		d.pdf.RectFromUpperLeftWithStyle(x, d.y, widths[i], height, "F")

		// Texto negro.
		d.pdf.SetTextColor(0, 0, 0)

		for lineIndex, line := range tableCellLines(v, widths[i], fontSize) {
			d.pdf.SetX(x + 4)
			d.pdf.SetY(d.y + 4 + float64(lineIndex*11))
			_ = d.pdf.CellWithOption(&gopdf.Rect{W: widths[i] - 8, H: 11}, line, gopdf.CellOption{Align: gopdf.Left})
		}

		// Borde negro.
		d.pdf.SetStrokeColor(0, 0, 0)
		d.pdf.RectFromUpperLeftWithStyle(x, d.y, widths[i], height, "D")
		x += widths[i]
	}
	d.pdf.SetTextColor(0, 0, 0)
	d.y += height
}

// ─── Firmas ──────────────────────────────────────────────────────────────────

func (d *contractPDF) signatures(c domain.Content) {
	d.y += 45

	col1X := margin + 40.0
	col2X := pageWidth/2 + 40.0
	signatureSpace := 90.0

	operatorY := d.y
	clientY := d.y

	// Columna operadores.
	operatorY -= 12
	operatorY -= 35
	for _, p := range c.Representatives {
		d.ensure(signatureSpace)
		d.pdf.Line(col1X, operatorY, col1X+200, operatorY)
		operatorY -= 15

		d.setFont(true, fontSizeSignatureDetail)
		d.pdf.SetX(col1X)
		d.pdf.SetY(operatorY)
		_ = d.pdf.Cell(nil, "El Operador")
		operatorY -= 10

		d.setFont(false, fontSizeSignatureDetail)
		d.pdf.SetX(col1X)
		d.pdf.SetY(operatorY)
		_ = d.pdf.Cell(nil, fallback(p.Name))
		operatorY -= 13

		d.pdf.SetX(col1X)
		d.pdf.SetY(operatorY)
		_ = d.pdf.Cell(nil, "RUT "+formatRUT(fallback(p.DNI)))
		operatorY -= signatureSpace - 28
	}

	// Columna clientes.
	clientY -= 47
	for _, p := range c.ClientRepresentatives {
		d.ensure(signatureSpace)
		d.pdf.Line(col2X, clientY, col2X+200, clientY)
		clientY -= 15

		d.setFont(true, fontSizeSignatureDetail)
		d.pdf.SetX(col2X)
		d.pdf.SetY(clientY)
		_ = d.pdf.Cell(nil, "El Representante")
		clientY -= 10

		d.setFont(false, fontSizeSignatureDetail)
		d.pdf.SetX(col2X)
		d.pdf.SetY(clientY)
		_ = d.pdf.Cell(nil, fallback(p.Name))
		clientY -= 14

		d.pdf.SetX(col2X)
		d.pdf.SetY(clientY)
		_ = d.pdf.Cell(nil, "RUT "+formatRUT(fallback(p.DNI)))
		clientY -= signatureSpace - 31
	}
}

// ─── Funciones auxiliares ────────────────────────────────────────────────────

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

// money formatea un monto con separador de miles (formato chileno).
// Ejemplo: 1234567 → "$1.234.567"
func money(v int64) string {
	s := fmt.Sprintf("%d", v)
	if v < 0 {
		s = s[1:] // quitar el signo para formatear
	}
	n := len(s)
	if n <= 3 {
		if v < 0 {
			return "-$" + s
		}
		return "$" + s
	}
	var result strings.Builder
	remainder := n % 3
	if remainder > 0 {
		result.WriteString(s[:remainder])
	}
	for i := remainder; i < n; i += 3 {
		if result.Len() > 0 {
			result.WriteByte('.')
		}
		result.WriteString(s[i : i+3])
	}
	if v < 0 {
		return "-$" + result.String()
	}
	return "$" + result.String()
}

// Meses en español para formateo de fechas.
var spanishMonths = []string{
	"enero", "febrero", "marzo", "abril", "mayo", "junio",
	"julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre",
}

// displayDate devuelve la fecha en formato simple: "1 de septiembre de 2026".
func displayDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback(value)
	}
	if date, err := time.Parse(time.RFC3339Nano, value); err == nil {
		d := date.UTC()
		return fmt.Sprintf("%d de %s de %d", d.Day(), spanishMonths[d.Month()-1], d.Year())
	}
	if date, err := time.Parse(time.RFC3339, value); err == nil {
		d := date.UTC()
		return fmt.Sprintf("%d de %s de %d", d.Day(), spanishMonths[d.Month()-1], d.Year())
	}
	return fallback(value)
}

// Días de la semana en español.
var spanishWeekdays = []string{
	"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado",
}

// displayDateFull devuelve la fecha en formato completo: "lunes 1 de septiembre de 2026".
func displayDateFull(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback(value)
	}
	if date, err := time.Parse(time.RFC3339Nano, value); err == nil {
		d := date.UTC()
		return fmt.Sprintf("%s %d de %s de %d", spanishWeekdays[d.Weekday()], d.Day(), spanishMonths[d.Month()-1], d.Year())
	}
	if date, err := time.Parse(time.RFC3339, value); err == nil {
		d := date.UTC()
		return fmt.Sprintf("%s %d de %s de %d", spanishWeekdays[d.Weekday()], d.Day(), spanishMonths[d.Month()-1], d.Year())
	}
	return fallback(value)
}

// displayDateShort devuelve la fecha en formato corto DD/MM/YYYY (para tablas).
func displayDateShort(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback(value)
	}
	if date, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return date.UTC().Format("02/01/2006")
	}
	if date, err := time.Parse(time.RFC3339, value); err == nil {
		return date.UTC().Format("02/01/2006")
	}
	return fallback(value)
}

// formatRUT formatea un RUT chileno con puntos y guión.
// Ejemplo: "12345678-9" → "12.345.678-9"
func formatRUT(rut string) string {
	clean := strings.ReplaceAll(strings.ReplaceAll(rut, ".", ""), " ", "")
	if !strings.Contains(clean, "-") || len(clean) < 3 {
		return rut
	}
	parts := strings.SplitN(clean, "-", 2)
	body := parts[0]
	dv := parts[1]

	// Agregar puntos al cuerpo.
	var formatted strings.Builder
	n := len(body)
	remainder := n % 3
	if remainder > 0 {
		formatted.WriteString(body[:remainder])
	}
	for i := remainder; i < n; i += 3 {
		if formatted.Len() > 0 {
			formatted.WriteByte('.')
		}
		formatted.WriteString(body[i : i+3])
	}
	return formatted.String() + "-" + dv
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

// tableCellLines divide el texto de una celda en líneas que quepan en el ancho
// dado, usando un estimado de ancho por carácter para la fuente del tamaño
// indicado.
func tableCellLines(value string, width float64, fontSize int) []string {
	charWidth := float64(fontSize) * 0.5
	max := int(width / charWidth)
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

// wrap divide texto por conteo de caracteres (solo para celdas de tabla donde
// no se justifica). Para párrafos se usa wrapByWidth.
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
