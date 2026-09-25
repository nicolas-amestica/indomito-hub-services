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
