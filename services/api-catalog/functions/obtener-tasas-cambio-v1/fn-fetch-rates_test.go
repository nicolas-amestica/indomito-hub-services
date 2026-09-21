package rates

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

// Ninguno de estos tests toca la red: la fuente se sustituye por un
// httptest.Server, que escucha en loopback y muere con el test.

// stubResponse es una respuesta del servidor sustituto.
type stubResponse struct {
	status int
	body   string
}

// okDocument arma el documento que la fuente devuelve para una divisa.
func okDocument(currency, date string, toClp float64) stubResponse {
	return stubResponse{
		status: http.StatusOK,
		body:   fmt.Sprintf(`{"date":%q,%q:{"eur":0.91,"clp":%v}}`, date, currency, toClp),
	}
}

// stubSource es la fuente externa sustituida. Devuelve, por divisa, las
// respuestas configuradas en orden; agotada la lista repite la última, que es
// lo que permite describir una falla permanente con un solo elemento.
type stubSource struct {
	t         *testing.T
	responses map[string][]stubResponse

	mu       sync.Mutex
	requests map[string]int

	server *httptest.Server
}

// newStubSource levanta el servidor y devuelve la fuente que apunta a él.
func newStubSource(t *testing.T, responses map[string][]stubResponse) *stubSource {
	t.Helper()

	stub := &stubSource{
		t:         t,
		responses: responses,
		requests:  make(map[string]int),
	}

	stub.server = httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(stub.server.Close)

	return stub
}

// serve responde el documento que corresponde al pedido, contando los intentos
// por divisa.
func (s *stubSource) serve(w http.ResponseWriter, r *http.Request) {
	currency := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".json")

	s.mu.Lock()
	index := s.requests[currency]
	s.requests[currency]++
	s.mu.Unlock()

	configured, ok := s.responses[currency]
	if !ok || len(configured) == 0 {
		s.t.Errorf("el handler pidio %q y no hay respuestas configuradas", currency)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if index >= len(configured) {
		index = len(configured) - 1
	}

	response := configured[index]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.status)
	_, _ = w.Write([]byte(response.body))
}

// source es la Source que se le pasa a FetchRates.
func (s *stubSource) source() Source {
	return Source{HTTPClient: s.server.Client(), BaseURL: s.server.URL}
}

// requestCount son los pedidos recibidos para una divisa.
func (s *stubSource) requestCount(currency string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.requests[currency]
}

// observedLogger devuelve un logger que retiene lo que se le escribe, para
// comprobar los campos de una advertencia.
func observedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.WarnLevel)
	return zap.New(core), logs
}

// Feature: program-form, Property 33: Las tasas se redondean al entero más cercano.
func TestFetchRatesRoundsToNearestInteger(t *testing.T) {
	// Requirement 14.3: los dos valores se redondean al entero mas cercano, uno
	// hacia arriba y otro hacia abajo.
	tests := []struct {
		name    string
		usd     float64
		brl     float64
		wantUSD int64
		wantBRL int64
	}{
		{name: "hacia abajo", usd: 947.31, brl: 168.20, wantUSD: 947, wantBRL: 168},
		{name: "hacia arriba", usd: 947.51, brl: 168.72, wantUSD: 948, wantBRL: 169},
		{name: "medio exacto", usd: 947.5, brl: 168.5, wantUSD: 948, wantBRL: 169},
		{name: "entero", usd: 950, brl: 170, wantUSD: 950, wantBRL: 170},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := newStubSource(t, map[string][]stubResponse{
				currencyUSD: {okDocument(currencyUSD, "2026-03-14", test.usd)},
				currencyBRL: {okDocument(currencyBRL, "2026-03-14", test.brl)},
			})

			rates, err := FetchRates(context.Background(), stub.source(), zap.NewNop())
			if err != nil {
				t.Fatalf("FetchRates devolvio error: %v", err)
			}

			if rates.UsdToClp != test.wantUSD {
				t.Errorf("UsdToClp = %d, se esperaba %d", rates.UsdToClp, test.wantUSD)
			}
			if rates.BrlToClp != test.wantBRL {
				t.Errorf("BrlToClp = %d, se esperaba %d", rates.BrlToClp, test.wantBRL)
			}
			if rates.Date != "2026-03-14" {
				t.Errorf("Date = %q, se esperaba la fecha de la fuente", rates.Date)
			}
			if rates.Attempts != 1 {
				t.Errorf("Attempts = %d, una consulta exitosa no debe reintentar", rates.Attempts)
			}
			if rates.Snapshot().IsFallback {
				t.Error("una consulta exitosa no puede marcarse como respaldo")
			}
		})
	}
}

// Feature: program-form, Property 36: La fecha de la respuesta es siempre la del USD.
func TestFetchRatesUsesUsdDateWhenDatesDiverge(t *testing.T) {
	// Requirement 14.12: la fecha de la respuesta es la del USD, y la
	// divergencia se registra con las dos fechas.
	stub := newStubSource(t, map[string][]stubResponse{
		currencyUSD: {okDocument(currencyUSD, "2026-03-14", 947.31)},
		currencyBRL: {okDocument(currencyBRL, "2026-03-13", 168.20)},
	})

	log, logs := observedLogger()

	rates, err := FetchRates(context.Background(), stub.source(), log)
	if err != nil {
		t.Fatalf("FetchRates devolvio error: %v", err)
	}

	if rates.Date != "2026-03-14" {
		t.Errorf("Date = %q, se esperaba la fecha informada para USD", rates.Date)
	}

	entries := logs.FilterMessage(divergentDatesEvent).All()
	if len(entries) != 1 {
		t.Fatalf("se registraron %d advertencias de fechas divergentes, se esperaba 1", len(entries))
	}

	fields := entries[0].ContextMap()
	if fields["usdDate"] != "2026-03-14" {
		t.Errorf("usdDate = %v, se esperaba 2026-03-14", fields["usdDate"])
	}
	if fields["brlDate"] != "2026-03-13" {
		t.Errorf("brlDate = %v, se esperaba 2026-03-13", fields["brlDate"])
	}
}

func TestFetchRatesDoesNotWarnWhenDatesMatch(t *testing.T) {
	stub := newStubSource(t, map[string][]stubResponse{
		currencyUSD: {okDocument(currencyUSD, "2026-03-14", 947.31)},
		currencyBRL: {okDocument(currencyBRL, "2026-03-14", 168.20)},
	})

	log, logs := observedLogger()

	if _, err := FetchRates(context.Background(), stub.source(), log); err != nil {
		t.Fatalf("FetchRates devolvio error: %v", err)
	}

	if count := logs.FilterMessage(divergentDatesEvent).Len(); count != 0 {
		t.Errorf("se registraron %d advertencias con fechas iguales, se esperaba 0", count)
	}
}

func TestFetchRatesRetriesUntilSuccess(t *testing.T) {
	// Requirement 14.6: una falla transitoria no debe llegar al cliente.
	stub := newStubSource(t, map[string][]stubResponse{
		currencyUSD: {
			{status: http.StatusBadGateway, body: `{"error":"bad gateway"}`},
			okDocument(currencyUSD, "2026-03-14", 947.31),
		},
		currencyBRL: {okDocument(currencyBRL, "2026-03-14", 168.20)},
	})

	rates, err := FetchRates(context.Background(), stub.source(), zap.NewNop())
	if err != nil {
		t.Fatalf("FetchRates devolvio error: %v", err)
	}

	if rates.Attempts != 2 {
		t.Errorf("Attempts = %d, se esperaba 2", rates.Attempts)
	}
	if rates.UsdToClp != 947 {
		t.Errorf("UsdToClp = %d, se esperaba 947", rates.UsdToClp)
	}
}

func TestFetchRatesFailsAfterMaxAttempts(t *testing.T) {
	// Requirement 14.7: una respuesta sin el valor en CLP es una consulta
	// fallida, y agotados los intentos el error es de upstream.
	tests := []struct {
		name string
		brl  stubResponse
	}{
		{
			name: "sin el valor en pesos",
			brl:  stubResponse{status: http.StatusOK, body: `{"date":"2026-03-14","brl":{"eur":0.16}}`},
		},
		{
			name: "sin la divisa consultada",
			brl:  stubResponse{status: http.StatusOK, body: `{"date":"2026-03-14"}`},
		},
		{
			name: "sin fecha",
			brl:  stubResponse{status: http.StatusOK, body: `{"brl":{"clp":168.20}}`},
		},
		{
			name: "valor no positivo",
			brl:  stubResponse{status: http.StatusOK, body: `{"date":"2026-03-14","brl":{"clp":0}}`},
		},
		{
			name: "cuerpo ilegible",
			brl:  stubResponse{status: http.StatusOK, body: `no es json`},
		},
		{
			name: "estado de error",
			brl:  stubResponse{status: http.StatusServiceUnavailable, body: `{}`},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := newStubSource(t, map[string][]stubResponse{
				currencyUSD: {okDocument(currencyUSD, "2026-03-14", 947.31)},
				currencyBRL: {test.brl},
			})

			rates, err := FetchRates(context.Background(), stub.source(), zap.NewNop())
			if err == nil {
				t.Fatalf("FetchRates no devolvio error y entrego %+v", rates)
			}

			appErr := apperr.From(err)
			if appErr.Code() != apperr.CodeUpstreamServiceError {
				t.Errorf("Code = %q, se esperaba %q", appErr.Code(), apperr.CodeUpstreamServiceError)
			}
			if appErr.HTTPStatus() != http.StatusBadGateway {
				t.Errorf("HTTPStatus = %d, se esperaba 502", appErr.HTTPStatus())
			}

			if got := stub.requestCount(currencyBRL); got != ratesMaxAttempts {
				t.Errorf("se hicieron %d intentos sobre BRL, se esperaban %d", got, ratesMaxAttempts)
			}
		})
	}
}

func TestFetchRatesCancelsSiblingWhenOneCurrencyFails(t *testing.T) {
	// Requirement 14.7: si una divisa ya fallo, la que sigue en vuelo no tiene
	// para que consumir su limite de 10 s.
	brlStarted := make(chan struct{}, ratesMaxAttempts)
	cancelled := make(chan struct{}, ratesMaxAttempts)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, currencyUSD) {
			// Cada intento espera a que BRL este realmente en vuelo antes de
			// fallar. Sin esta barrera, la cancelacion puede ocurrir antes de
			// que el transporte llegue a enviar la peticion hermana, y el test
			// confunde una cancelacion aun mas temprana con una que no ocurrio.
			select {
			case <-brlStarted:
			case <-time.After(2 * time.Second):
				t.Error("la peticion de BRL no alcanzo a iniciar")
			}
			w.WriteHeader(http.StatusBadGateway)
			return
		}

		// BRL no responde nunca por su cuenta: termina cuando el contexto de su
		// peticion se cancela, que es exactamente lo que se quiere comprobar.
		brlStarted <- struct{}{}
		select {
		case <-r.Context().Done():
			cancelled <- struct{}{}
		case <-time.After(2 * time.Second):
			t.Error("la peticion de BRL no fue cancelada al fallar USD")
		}
	}))
	defer server.Close()

	source := Source{HTTPClient: server.Client(), BaseURL: server.URL}

	start := time.Now()
	if _, err := FetchRates(context.Background(), source, zap.NewNop()); err == nil {
		t.Fatal("FetchRates no devolvio error")
	}
	elapsed := time.Since(start)

	// Se espera la senal en vez de contar el buffer: el handler observa la
	// cancelacion en su propia goroutine y puede llegar despues de que Do
	// devuelva del lado del cliente.
	for i := range ratesMaxAttempts {
		select {
		case <-cancelled:
		case <-time.After(2 * time.Second):
			t.Fatalf("solo se cancelaron %d de %d peticiones de BRL", i, ratesMaxAttempts)
		}
	}

	// Las dos esperas del backoff mas el trabajo del handler: muy por debajo de
	// los 10 s que habria consumido cada intento sin la cancelacion.
	if elapsed > 5*time.Second {
		t.Errorf("los intentos tardaron %s: la cancelacion no esta cortando la divisa hermana", elapsed)
	}
}

func TestFetchRatesStopsOnCancelledContext(t *testing.T) {
	// La cancelacion de la invocacion gana sobre la politica de reintentos.
	stub := newStubSource(t, map[string][]stubResponse{
		currencyUSD: {okDocument(currencyUSD, "2026-03-14", 947.31)},
		currencyBRL: {okDocument(currencyBRL, "2026-03-14", 168.20)},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := FetchRates(ctx, stub.source(), zap.NewNop())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("FetchRates no devolvio error con el contexto cancelado")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("el error no envuelve context.Canceled: %v", err)
	}
	if elapsed > backoffFor(1) {
		t.Errorf("FetchRates tardo %s: la espera entre intentos ignoro la cancelacion", elapsed)
	}
	if got := stub.requestCount(currencyUSD); got != 0 {
		t.Errorf("se hicieron %d pedidos con el contexto ya cancelado, se esperaban 0", got)
	}
}

func TestBackoffGrowsByFactorTwo(t *testing.T) {
	want := []time.Duration{ratesBackoff, ratesBackoff * ratesBackoffFactor}

	for attempt, expected := range want {
		if got := backoffFor(attempt + 1); got != expected {
			t.Errorf("backoffFor(%d) = %s, se esperaba %s", attempt+1, got, expected)
		}
	}
}

func TestDefaultSourcePointsToJsDelivr(t *testing.T) {
	source := DefaultSource()

	if source.BaseURL != ratesBaseURL {
		t.Errorf("BaseURL = %q, se esperaba %q", source.BaseURL, ratesBaseURL)
	}
	if source.HTTPClient == nil {
		t.Error("DefaultSource debe traer un cliente HTTP")
	}

	// Una Source vacia se completa con los mismos valores, para que el llamador
	// no pueda dejarla a medias.
	completed := Source{}.withDefaults()
	if completed.BaseURL != ratesBaseURL || completed.HTTPClient == nil {
		t.Errorf("withDefaults dejo la fuente incompleta: %+v", completed)
	}
}

func TestFetchedRatesSnapshotIsNotFallback(t *testing.T) {
	// Requirement 14.5.
	rates := FetchedRates{
		Date:            "2026-03-14",
		UsdToClp:        947,
		BrlToClp:        168,
		Attempts:        1,
		UpstreamLatency: 1500 * time.Millisecond,
	}

	snapshot := rates.Snapshot()
	if snapshot.IsFallback {
		t.Error("IsFallback = true en tasas obtenidas de la fuente")
	}
	if snapshot.Date != rates.Date || snapshot.UsdToClp != rates.UsdToClp || snapshot.BrlToClp != rates.BrlToClp {
		t.Errorf("Snapshot() = %+v, no refleja %+v", snapshot, rates)
	}
	if rates.UpstreamLatencyMs() != 1500 {
		t.Errorf("UpstreamLatencyMs = %d, se esperaba 1500", rates.UpstreamLatencyMs())
	}
}
