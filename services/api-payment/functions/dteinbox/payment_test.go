package dteinbox

import (
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
	"testing"
)

func TestRegistryRestriction(t *testing.T) {
	code := 27
	for _, tc := range []struct {
		name     string
		document Document
		blocked  bool
	}{
		{"contado", Document{PaymentForm: 1}, true},
		{"credito", Document{PaymentForm: 2}, false},
		{"sin costo", Document{PaymentForm: 3}, true},
		{"historico sin dato", Document{}, false},
		{"historico codigo SII", Document{Registry: &sii.ReceivedResult{Code: &code}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (tc.document.RegistryRestriction() != "") != tc.blocked {
				t.Fatal("restricción incorrecta")
			}
		})
	}
}
