package functions

import (
	"bytes"
	"ind-hub-api-gox-sls-pri-gh/services/api-contract/domain"
	"strings"
	"testing"
)

func TestComposePDFIncludesPassengerSection(t *testing.T) {
	content := domain.Content{Passengers: []domain.Passenger{{Names: "Ana", LastNames: "Perez", DNI: "12345678-5", BirthDate: "01/01/2010", Nationality: "Chilena", Sex: "FEMALE"}}}
	pdf, err := ComposePDF(content, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("invalid PDF header: %q", pdf[:4])
	}
	if len(pdf) < 1000 {
		t.Fatalf("unexpectedly small PDF: %d", len(pdf))
	}
}

func TestClauseOneSupportsUndefinedTravelDates(t *testing.T) {
	content := domain.Content{}
	text := buildClauses(content)[0].text
	if !strings.Contains(text, "fecha por definir") || !strings.Contains(text, "anexo") {
		t.Fatalf("missing undefined date wording: %s", text)
	}
	content.Trip.DepartureDate = "2027-01-10T00:00:00Z"
	content.Trip.ReturnDate = "2027-01-14T00:00:00Z"
	if text = buildClauses(content)[0].text; strings.Contains(text, "fecha por definir") {
		t.Fatalf("known dates must retain dated clause: %s", text)
	}
}

func TestCurrentRefundPolicyIsExplicitAndPreservesHistoricalClause(t *testing.T) {
	c := domain.Content{}
	c.Payments.Conditions.CancellationPenaltyPercentage = 25
	if !strings.Contains(buildClauses(c)[20].text, "25% del valor total") {
		t.Fatal("historical clause changed")
	}
	domain.UseCurrentTerms(&c)
	text := buildClauses(c)[20].text
	for _, term := range []string{"abono inicial efectivamente pagado no será reembolsable", "No se establece un porcentaje fijo", "cuotas", "por escrito", "excluido el abono inicial", "derechos irrenunciables", "pagos duplicados"} {
		if !strings.Contains(text, term) {
			t.Fatalf("missing %q: %s", term, text)
		}
	}
	if strings.Contains(text, "25%") || strings.Contains(text, "0% del valor total") {
		t.Fatal("legacy penalty remains")
	}
	if !strings.Contains(buildClauses(c)[17].text, "no modificará automáticamente el valor de las cuotas") {
		t.Fatal("remaining passenger amount not protected")
	}
}

func TestPaymentClauseIncludesCompleteDueDateAndShortMonthRule(t *testing.T) {
	c := domain.Content{Payments: domain.Payments{Installments: domain.Installments{Quantity: 5, StartYear: 2027, StartMonth: "01", StartDay: 31}}}
	text := buildClauses(c)[16].text
	for _, term := range []string{"31 de enero de 2027", "día 31 de cada mes", "último día de ese mes", "meses siguientes"} {
		if !strings.Contains(text, term) {
			t.Fatalf("missing %q: %s", term, text)
		}
	}
}

func TestFormatRUTAddsDotsAndHyphen(t *testing.T) {
	for input, expected := range map[string]string{
		"12345678-5":   "12.345.678-5",
		"12.345.678-5": "12.345.678-5",
		"7654796-4":    "7.654.796-4",
	} {
		if got := formatRUT(input); got != expected {
			t.Errorf("formatRUT(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestContractTextFormatsEveryRUTSource(t *testing.T) {
	content := domain.Content{
		Representatives:       []domain.Person{{Name: "Operador", DNI: "7654796-4"}},
		ClientRepresentatives: []domain.Person{{Name: "Cliente", DNI: "12345678-5"}},
		Payments:              domain.Payments{BankAccount: domain.BankAccount{HolderDNI: "77654796-4"}},
	}
	general := buildGeneral(content)
	if !strings.Contains(general, "7.654.796-4") || !strings.Contains(general, "12.345.678-5") || !strings.Contains(general, "77.654.796-4") {
		t.Fatalf("general text has unformatted RUTs: %q", general)
	}
	if !strings.Contains(buildClauses(content)[16].text, "77.654.796-4") {
		t.Fatal("payment holder RUT was not formatted in clause 17")
	}
}

func TestClauseSeventeenMentionsCashDiscountOnlyWhenPositive(t *testing.T) {
	content := domain.Content{}
	if text := buildClauses(content)[16].text; strings.Contains(text, "descuento") {
		t.Fatalf("zero discount must preserve the original clause: %q", text)
	}
	content.Payments.DiscountPercentage = 12.5
	text := buildClauses(content)[16].text
	if !strings.Contains(text, "descuento de 12.5%") || !strings.Contains(text, "única y exclusivamente") || !strings.Contains(text, "efectivo") {
		t.Fatalf("cash discount is missing from clause 17: %q", text)
	}
}

func TestClauseSeventeenStatesGroupAndIndividualMonthlyInstallments(t *testing.T) {
	content := domain.Content{Payments: domain.Payments{
		TotalPassengers: 30,
		FreePassengers:  2,
		PricePerPerson:  100000,
		TotalGroup:      3000000,
		DownPayment:     0,
		GroupBalance:    3000000,
		Installments: domain.Installments{
			Quantity:                   5,
			GroupInstallmentValue:      600000,
			IndividualInstallmentValue: 20000,
			StartMonth:                 "2027-03",
		},
	}}

	text := buildClauses(content)[16].text
	for _, expected := range []string{"5 cuotas mensuales", "$600.000", "$20.000", "por cada pasajero pagante"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("clause 17 does not contain %q: %q", expected, text)
		}
	}
}

func TestPassengerTableLayoutFitsSixtyRowsOnOnePage(t *testing.T) {
	rowHeight, fontSize := passengerTableLayout(60, 179)
	if got := 179 + 61*rowHeight; got > 710.001 {
		t.Fatalf("table bottom = %.2f, exceeds page limit", got)
	}
	if fontSize < 5 {
		t.Fatalf("font size = %d, must remain legible", fontSize)
	}
}

func TestComposePDFSupportsSixtyNumberedPassengers(t *testing.T) {
	rows := make([]domain.Passenger, 60)
	for index := range rows {
		rows[index] = domain.Passenger{Names: "Pasajero", LastNames: "Apellido", DNI: "12345678-5", BirthDate: "2010-01-01", Nationality: "Chilena", Sex: "FEMALE"}
	}
	pdf, err := ComposePDF(domain.Content{Passengers: rows}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatal("invalid PDF")
	}
}

func TestComposeAmendmentPDFSupportsUndefinedAndDefinedDates(t *testing.T) {
	content := domain.Content{
		Institution:           domain.Institution{Name: "Colegio Prueba"},
		Representatives:       []domain.Person{{Name: "Operador", DNI: "16915292-6"}},
		ClientRepresentatives: []domain.Person{{Name: "Apoderado", DNI: "12345678-5"}},
	}
	amendment := domain.ContractAmendment{
		ContractID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", BaseContractVersion: 4,
		Reason: "Definir fechas del viaje", Before: domain.ContractTermsSnapshot{Days: 4, Nights: 3, Services: []domain.Service{{Description: "Transporte"}}},
		After: domain.ContractTermsSnapshot{DepartureDate: "2027-10-05T00:00:00Z", ReturnDate: "2027-10-08T00:00:00Z", Days: 4, Nights: 3, Services: []domain.Service{{Description: "Transporte"}, {Description: "Excursión"}}},
	}
	if got := amendmentDates(amendment.Before); !strings.Contains(got, "por definir") {
		t.Fatalf("undefined date wording missing: %q", got)
	}
	if got := amendmentDates(amendment.After); !strings.Contains(got, "5 de octubre de 2027") || !strings.Contains(got, "8 de octubre de 2027") {
		t.Fatalf("defined dates missing: %q", got)
	}
	pdf, err := ComposeAmendmentPDF(content, amendment)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) || len(pdf) < 1000 {
		t.Fatalf("invalid amendment PDF: %d bytes", len(pdf))
	}
}
