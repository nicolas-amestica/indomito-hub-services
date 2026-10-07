package functions

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseSMTPConfigRequiresDevSenderAndTLSSubmissionPort(t *testing.T) {
	valid := `{"host":"smtp.example.test","port":587,"user":"no-reply-dev@girasindomito.cl","password":"secret","from":"Giras Indomito <no-reply-dev@girasindomito.cl>"}`
	config, err := parseSMTPConfig([]byte(valid))
	if err != nil || config.Port != 587 {
		t.Fatalf("valid config rejected: %v", err)
	}
	for _, invalid := range []string{
		strings.Replace(valid, "587", "993", 1),
		strings.Replace(valid, "no-reply-dev@girasindomito.cl>\"", "other@girasindomito.cl>\"", 1),
		strings.Replace(valid, "smtp.example.test", "smtp.example.test\\r\\nBcc: attacker@example.test", 1),
		`{"host":"smtp.example.test"}`,
	} {
		if _, err = parseSMTPConfig([]byte(invalid)); err == nil {
			t.Fatal("unsafe SMTP config accepted")
		}
	}
}

func TestBuildMailMessageEncodesHeadersAndPDF(t *testing.T) {
	message, err := buildMailMessage("Giras Indomito <no-reply-dev@girasindomito.cl>", "family@example.test", "receipt", "delivery", []byte("pdf"), false)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err = message.WriteTo(&output); err != nil {
		t.Fatal(err)
	}
	raw := output.String()
	for _, expected := range []string{"no-reply-dev@girasindomito.cl", "family@example.test", "application/pdf", "comprobante-receipt.pdf", "no es una boleta ni factura"} {
		if !strings.Contains(raw, expected) {
			t.Fatalf("message missing %q", expected)
		}
	}
	if _, err = buildMailMessage("no-reply-dev@girasindomito.cl", "Display <family@example.test>", "receipt", "delivery", nil, false); err == nil {
		t.Fatal("display recipient accepted")
	}
}
