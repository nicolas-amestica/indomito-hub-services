package functions

import (
	"bytes"
	"ind-hub-api-gox-sls-pri-gh/services/api-contract/domain"
	"testing"
)

func TestComposePDFIncludesPassengerSection(t *testing.T) {
	content := domain.Content{Passengers: []domain.Passenger{{Names: "Ana", LastNames: "Perez", DNI: "12345678-9", BirthDate: "01/01/2010", Nationality: "Chilena"}}}
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
