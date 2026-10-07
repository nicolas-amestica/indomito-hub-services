package functions

import (
	"os"
	"testing"
)

// TestRenderReceiptFixture permite inspección visual explícita sin generar artefactos durante pruebas normales.
func TestRenderReceiptFixture(t *testing.T) {
	path := os.Getenv("RENDER_RECEIPT_PATH")
	if path == "" {
		t.Skip("RENDER_RECEIPT_PATH no configurado")
	}
	t.Setenv("RECEIPT_VERIFICATION_URL", "https://pagos.dev.girasindomito.cl/verificar-comprobante")
	row := validReceiptRow()
	row.DocumentVersion = 3
	row.PassengerName = "Ana Prueba"
	row.PassengerDocument = "12.345.678-5"
	model, err := buildReceiptModel(row)
	if err != nil {
		t.Fatal(err)
	}
	document, err := renderReceipt(model)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, document.Bytes, 0o600); err != nil {
		t.Fatal(err)
	}
}
