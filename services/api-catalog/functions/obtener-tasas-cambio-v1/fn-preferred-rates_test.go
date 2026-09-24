package rates

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
)

func TestFetchPreferredRatesUsesBancoCentralFirst(t *testing.T) {
	var mutex sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		requests[r.URL.Path]++
		mutex.Unlock()

		if r.URL.Path != "/bcch" {
			t.Errorf("se consultó el respaldo pese a responder Banco Central: %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		if r.URL.Query().Get("token") != "token-prueba" {
			t.Error("la consulta no incluyó el token configurado")
		}
		series := r.URL.Query().Get("timeseries")
		value := "947.51"
		if series == bcchBRLSeries {
			value = "168.20"
		}
		_, _ = fmt.Fprintf(w, `{"Codigo":0,"Descripcion":"Success","Series":{"Obs":[{"indexDateString":"22-09-2026","value":"","statusCode":"ND"},{"indexDateString":"23-09-2026","value":%q,"statusCode":"OK"}]}}`, value)
	}))
	defer server.Close()

	rates, err := FetchPreferredRates(context.Background(), PreferredSources{
		HTTPClient:       server.Client(),
		BCCHToken:        "token-prueba",
		BCCHURL:          server.URL + "/bcch",
		CurrencyFallback: Source{HTTPClient: server.Client(), BaseURL: server.URL},
		Now:              func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) },
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("FetchPreferredRates devolvió error: %v", err)
	}

	if rates.Date != "2026-09-23" || rates.UsdToClp != 948 || rates.BrlToClp != 168 || rates.Source != program.ExchangeRateSourceBCCH {
		t.Errorf("tasas Banco Central = %+v", rates)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if requests["/bcch"] != 2 || requests["/usd.json"] != 0 || requests["/brl.json"] != 0 {
		t.Errorf("peticiones por ruta = %+v", requests)
	}
}

func TestFetchPreferredRatesUsesCurrencyAPIFallbackWithoutToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usd.json":
			_, _ = w.Write([]byte(`{"date":"2026-09-23","usd":{"clp":947.51}}`))
		case "/brl.json":
			_, _ = w.Write([]byte(`{"date":"2026-09-23","brl":{"clp":168.20}}`))
		case "/bcch":
			t.Error("no debe consultar Banco Central sin token")
			http.Error(w, "unexpected", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	rates, err := FetchPreferredRates(context.Background(), PreferredSources{
		HTTPClient:       server.Client(),
		BCCHURL:          server.URL + "/bcch",
		CurrencyFallback: Source{HTTPClient: server.Client(), BaseURL: server.URL},
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("FetchPreferredRates devolvió error: %v", err)
	}

	if rates.Date != "2026-09-23" || rates.UsdToClp != 948 || rates.BrlToClp != 168 || rates.Source != program.ExchangeRateSourceCurrencyAPI {
		t.Errorf("tasas de respaldo = %+v", rates)
	}
	if rates.Snapshot().IsFallback {
		t.Error("una tasa fresca de proveedor secundario no es el snapshot de DynamoDB")
	}
}

func TestDecodeBCCHQuoteRejectsResponseWithoutValidObservation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "código de error", body: `{"Codigo":1,"Descripcion":"Invalid token"}`},
		{name: "sin observaciones", body: `{"Codigo":0,"Series":{"Obs":[]}}`},
		{name: "valor no numérico", body: `{"Codigo":0,"Series":{"Obs":[{"indexDateString":"23-09-2026","value":"ND","statusCode":"OK"}]}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeBCCHQuote(strings.NewReader(test.body), bcchUSDSeries)
			if err == nil {
				t.Fatal("decodeBCCHQuote aceptó una respuesta inválida")
			}
		})
	}
}
