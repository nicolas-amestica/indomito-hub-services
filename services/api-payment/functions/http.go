package functions

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"
)

// Handler registra rutas de simulación, disponibles exclusivamente en loopback.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/pagos/accesos", a.access)
	mux.HandleFunc("POST /api/pagos/sesiones", a.login)
	mux.HandleFunc("DELETE /api/pagos/sesiones", a.logout)
	mux.HandleFunc("GET /api/pagos/cuotas", a.installments)
	mux.HandleFunc("POST /api/pagos/intentos", a.createPayment)
	mux.HandleFunc("POST /api/pagos/intentos/{id}/simular", a.simulate)
	mux.HandleFunc("GET /api/pagos/comprobantes/{id}", a.receipt)
	mux.HandleFunc("GET /api/pagos/tesoreria", a.treasury)
	mux.HandleFunc("POST /api/pagos/egresos", a.expense)
	mux.HandleFunc("POST /api/pagos/egresos/{id}/pagar", a.payExpense)
	mux.HandleFunc("POST /api/pagos/liquidaciones/{id}", a.settle)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			http.Error(w, "Solo entorno local", http.StatusForbidden)
			return
		}
		if r.Host != "localhost:4400" && r.Host != "127.0.0.1:8095" && r.Host != "localhost:8095" {
			http.Error(w, "Host no permitido", http.StatusForbidden)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && origin != "http://localhost:4400" && origin != "http://127.0.0.1:8095" {
			http.Error(w, "Origen no permitido", http.StatusForbidden)
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "Solicitud no permitida", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		a.mu.Lock()
		defer a.mu.Unlock()
		now := time.Now()
		for k, v := range a.sessions {
			if now.After(v.Expires) {
				delete(a.sessions, k)
			}
		}
		for k, v := range a.challenges {
			if now.After(v.Expires) {
				delete(a.challenges, k)
			}
		}
		window := a.rates[host]
		if now.Sub(window.Start) > time.Minute {
			window = rateWindow{Start: now}
		}
		window.Count++
		a.rates[host] = window
		if window.Count > 120 {
			w.Header().Set("Retry-After", "60")
			fail(w, 429)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func decode(r *http.Request, v any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("JSON requerido")
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("contenido adicional")
	}
	return nil
}
func write(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int) {
	write(w, status, map[string]string{"error": "No fue posible completar la solicitud."})
}
func secret() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func (a *App) passenger(r *http.Request) string {
	cookie, err := r.Cookie("payment-session")
	if err != nil {
		return ""
	}
	s, ok := a.sessions[cookie.Value]
	if !ok || time.Now().After(s.Expires) {
		return ""
	}
	return s.Passenger
}
func (a *App) admin(r *http.Request) bool {
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Local-Admin-Key")), []byte(a.adminKey)) == 1
}
func (a *App) access(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RUT string `json:"rut"`
	}
	if decode(r, &body) != nil || len(body.RUT) > 20 || len(a.challenges) >= 50 {
		fail(w, 400)
		return
	}
	id, err := secret()
	if err != nil {
		fail(w, 500)
		return
	}
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		fail(w, 500)
		return
	}
	code := big.NewInt(n.Int64() + 100000).String()
	rut := strings.ToUpper(strings.NewReplacer(".", "", "-", "", " ", "").Replace(body.RUT))
	passenger := ""
	if rut == "123456785" {
		passenger = "passenger-demo"
	}
	a.challenges[id] = challenge{Code: code, Passenger: passenger, Expires: time.Now().Add(5 * time.Minute)}
	// El código solo se revela para datos sintéticos; esta ruta no es desplegable.
	write(w, 202, map[string]string{"challengeId": id, "message": "Si el pasajero está registrado, recibirá un código en su contacto autorizado.", "demoCode": code})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChallengeID string `json:"challengeId"`
		Code        string `json:"code"`
	}
	if decode(r, &body) != nil {
		fail(w, 400)
		return
	}
	c, ok := a.challenges[body.ChallengeID]
	if !ok || time.Now().After(c.Expires) || c.Attempts >= 5 {
		fail(w, 401)
		return
	}
	c.Attempts++
	a.challenges[body.ChallengeID] = c
	if subtle.ConstantTimeCompare([]byte(c.Code), []byte(body.Code)) != 1 || c.Passenger == "" {
		fail(w, 401)
		return
	}
	token, err := secret()
	if err != nil {
		fail(w, 500)
		return
	}
	delete(a.challenges, body.ChallengeID)
	a.sessions[token] = session{Passenger: c.Passenger, Expires: time.Now().Add(15 * time.Minute)}
	http.SetCookie(w, &http.Cookie{Name: "payment-session", Value: token, Path: "/api/pagos", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 900})
	write(w, 200, map[string]string{"name": "Pasajero de demostración"})
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("payment-session"); err == nil {
		delete(a.sessions, c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "payment-session", Path: "/api/pagos", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	write(w, 200, map[string]bool{"ok": true})
}
