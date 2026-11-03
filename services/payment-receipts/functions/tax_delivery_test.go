package functions

import (
	"bytes"
	"testing"
)

func TestRenderTaxDocumentCreatesPDFWithElectronicStamp(t *testing.T) {
	document, err := renderTaxDocument(taxPublicDocument{
		Folio: 87, IssueDate: "2026-11-02", IssuerRUT: "77654796-4", IssuerName: "GIRAS INDOMITO LIMITADA",
		IssuerActivity: "Turismo", IssuerAddress: "Dirección 123", IssuerCommune: "Santiago",
		ResolutionDate: "2026-11-02", ResolutionNumber: 0, NetAmount: 10000, VATAmount: 1900, TotalAmount: 11900,
		TED:   `<TED version="1.0"><DD><RE>77654796-4</RE><TD>39</TD><F>87</F></DD></TED>`,
		Items: []taxPublicItem{{Description: "Cuota de gira", Quantity: 1, UnitPrice: 11900}},
	})
	if err != nil {
		t.Fatalf("render tax document: %v", err)
	}
	if len(document) < 1000 || !bytes.HasPrefix(document, []byte("%PDF-")) {
		t.Fatal("invalid tax PDF")
	}
}
