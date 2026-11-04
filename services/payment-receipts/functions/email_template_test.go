package functions

import (
	"strings"
	"testing"
)

func TestRenderBrandedEmailUsesSharedLayoutAndEscapesValues(t *testing.T) {
	html, err := renderBrandedEmail(brandedEmail{
		Preheader: "Tu documento está listo",
		Eyebrow:   "Comprobante de pago",
		Title:     "Recibimos tu pago",
		Lead:      "Operación registrada",
		Body:      "Adjuntamos el respaldo.",
		Notice:    "Conserva este correo.",
		Details:   []emailDetail{{Label: "Concepto", Value: `<script>alert("x")</script>`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Giras Indómito", "Dale una vuelta a la aventura", "Recibimos tu pago", "Comprobante de pago", "&lt;script&gt;"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("shared email missing %q", expected)
		}
	}
	if strings.Contains(html, "<script>") {
		t.Fatal("unescaped email value")
	}
}
