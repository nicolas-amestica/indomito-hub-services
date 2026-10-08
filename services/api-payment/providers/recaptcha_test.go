package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type recaptchaRoundTripFunc func(*http.Request) (*http.Response, error)

func (f recaptchaRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func recaptchaFixture(t *testing.T, response string) *Recaptcha {
	t.Helper()
	client, err := NewRecaptcha(RecaptchaConfig{
		ProjectID: "indomito-dev", SiteKey: "site-key", APIKey: "api-key",
		AllowedHostnames: []string{"pagos.dev.girasindomito.cl"}, MinimumScore: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	client.client = &http.Client{Transport: recaptchaRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || !strings.Contains(req.URL.Path, "/projects/indomito-dev/assessments") || req.URL.Query().Get("key") != "api-key" {
			t.Fatal("solicitud reCAPTCHA inválida")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})}
	return client
}

func TestRecaptchaAcceptsOnlyExpectedAssessment(t *testing.T) {
	valid := `{"tokenProperties":{"valid":true,"action":"khipu_checkout","hostname":"pagos.dev.girasindomito.cl"},"riskAnalysis":{"score":0.9}}`
	if err := recaptchaFixture(t, valid).Assess(context.Background(), strings.Repeat("t", 30), "khipu_checkout", "192.0.2.1", "test"); err != nil {
		t.Fatal(err)
	}
	for name, response := range map[string]string{
		"action":  `{"tokenProperties":{"valid":true,"action":"lookup","hostname":"pagos.dev.girasindomito.cl"},"riskAnalysis":{"score":0.9}}`,
		"host":    `{"tokenProperties":{"valid":true,"action":"khipu_checkout","hostname":"evil.example"},"riskAnalysis":{"score":0.9}}`,
		"score":   `{"tokenProperties":{"valid":true,"action":"khipu_checkout","hostname":"pagos.dev.girasindomito.cl"},"riskAnalysis":{"score":0.1}}`,
		"invalid": `{"tokenProperties":{"valid":false,"action":"khipu_checkout","hostname":"pagos.dev.girasindomito.cl"},"riskAnalysis":{"score":0.9}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := recaptchaFixture(t, response).Assess(context.Background(), strings.Repeat("t", 30), "khipu_checkout", "192.0.2.1", "test"); err == nil {
				t.Fatal("assessment inseguro aceptado")
			}
		})
	}
}
