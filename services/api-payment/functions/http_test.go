package functions

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func request(a *App, method, path, body, token, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:8095"+path, bytes.NewBufferString(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	r.Header.Set("X-Local-Admin-Key", key)
	if token != "" {
		r.AddCookie(&http.Cookie{Name: "payment-session", Value: token})
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func TestPaymentIsScopedIdempotentAndDurable(t *testing.T) {
	dir := t.TempDir()
	admin := strings.Repeat("a", 64)
	a, err := Open(dir, admin)
	if err != nil {
		t.Fatal(err)
	}
	a.sessions["demo"] = session{Passenger: "passenger-demo", Expires: time.Now().Add(time.Minute)}
	a.sessions["other"] = session{Passenger: "other", Expires: time.Now().Add(time.Minute)}
	installment := a.state.Installments[0]
	body := `{"installmentId":"` + installment.ID + `"}`
	if got := request(a, "GET", "/api/pagos/cuotas", "", "", ""); got.Code != 401 {
		t.Fatal("consulta anónima aceptada")
	}
	if got := request(a, "POST", "/api/pagos/intentos", body, "other", "idempotency-key-test"); got.Code != 404 {
		t.Fatal("acceso cruzado aceptado")
	}
	first := request(a, "POST", "/api/pagos/intentos", body, "demo", "idempotency-key-test")
	if first.Code != 201 {
		t.Fatalf("crear: %d %s", first.Code, first.Body)
	}
	repeat := request(a, "POST", "/api/pagos/intentos", body, "demo", "idempotency-key-test")
	if repeat.Code != 200 || len(a.state.Payments) != 1 {
		t.Fatal("se duplicó el intento")
	}
	id := a.state.Payments[0].ID
	path := "/api/pagos/intentos/" + id + "/simular"
	for n := 0; n < 2; n++ {
		if got := request(a, "POST", path, `{"outcome":"CONFIRMED"}`, "demo", ""); got.Code != 200 {
			t.Fatalf("confirmar: %d", got.Code)
		}
	}
	if len(a.state.Receipts) != 1 || len(a.state.Mail) != 1 || a.state.Installments[0].Paid != 20000 {
		t.Fatal("duplicación de cuota, comprobante o correo")
	}
	if got := request(a, "GET", "/api/pagos/comprobantes/"+a.state.Receipts[0].ID, "", "other", ""); got.Code != 404 {
		t.Fatal("comprobante expuesto")
	}
	restored, err := Open(dir, admin)
	if err != nil {
		t.Fatal(err)
	}
	if restored.state.Installments[0].Paid != 20000 {
		t.Fatal("pago perdido al reiniciar")
	}
	for n := 0; n < 2; n++ {
		if got := request(a, "POST", "/api/pagos/liquidaciones/"+id, `{}`, "", admin); got.Code != 200 {
			t.Fatal("liquidación fallida")
		}
	}
	count := 0
	for _, event := range a.state.Events {
		if event.Type == "payment.settled" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("liquidación duplicada")
	}
}
func TestSecurityAndOneTimeChallenge(t *testing.T) {
	a, err := Open(t.TempDir(), strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	response := request(a, "POST", "/api/pagos/accesos", `{"rut":"12.345.678-5"}`, "", "")
	var challenge map[string]string
	if err = json.Unmarshal(response.Body.Bytes(), &challenge); err != nil {
		t.Fatal(err)
	}
	body := `{"challengeId":"` + challenge["challengeId"] + `","code":"` + challenge["demoCode"] + `"}`
	if got := request(a, "POST", "/api/pagos/sesiones", body, "", ""); got.Code != 200 {
		t.Fatal("login fallido")
	}
	if got := request(a, "POST", "/api/pagos/sesiones", body, "", ""); got.Code != 401 {
		t.Fatal("código reutilizable")
	}
	for _, origin := range []string{"https://evil.example", "http://localhost:4000"} {
		r := httptest.NewRequest("POST", "http://localhost:8095/api/pagos/accesos", strings.NewReader(`{"rut":"12.345.678-5"}`))
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("origen externo aceptado")
		}
	}
}
func TestMultipleSupplierAdvances(t *testing.T) {
	admin := strings.Repeat("a", 64)
	a, err := Open(t.TempDir(), admin)
	if err != nil {
		t.Fatal(err)
	}
	id := a.state.Expenses[0].ID
	// Dos claves administrativas de prueba no se usan como idempotencia: headers explícitos.
	for _, key := range []string{"advance-one-123456", "advance-two-123456"} {
		r := httptest.NewRequest("POST", "http://localhost:8095/api/pagos/egresos/"+id+"/pagar", strings.NewReader(`{"amount":10000}`))
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Local-Admin-Key", admin)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("abono falló: %d", w.Code)
		}
	}
	if a.state.Expenses[0].Paid != 20000 {
		t.Fatal("abonos libres no acumulados")
	}
}
