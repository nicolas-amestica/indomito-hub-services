package sii

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestReceivedRegistryUsesTraditionalAuthAndBusinessCodes(t *testing.T) {
	for _, environment := range []Environment{EnvironmentCertification, EnvironmentProduction} {
		t.Run(string(environment), func(t *testing.T) {
			credentials := testCredentials(t, time.Now())
			credentials.Environment = environment
			calls := 0
			client := Client{Now: time.Now, HTTP: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				body, _ := io.ReadAll(req.Body)
				response := ""
				switch calls {
				case 1:
					host := "maullin.sii.cl"
					if environment == EnvironmentProduction {
						host = "palena.sii.cl"
					}
					if req.URL.Host != host || !strings.Contains(req.URL.Path, "CrSeed") {
						t.Fatalf("wrong seed endpoint %s", req.URL)
					}
					response = `<RESPUESTA><SEMILLA>12345</SEMILLA></RESPUESTA>`
				case 2:
					if !strings.Contains(string(body), "Signature") {
						t.Fatal("unsigned token")
					}
					response = `<RESPUESTA><TOKEN>test-token</TOKEN></RESPUESTA>`
				case 3:
					if req.Header.Get("Cookie") != "TOKEN=test-token" || !strings.Contains(string(body), "ingresarAceptacionReclamoDoc") || !strings.Contains(string(body), "<accionDoc>RCD</accionDoc>") {
						t.Fatal("wrong registry contract")
					}
					response = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><ingresarAceptacionReclamoDocResponse><return><codResp>8</codResp><descResp>Plazo vencido</descResp></return></ingresarAceptacionReclamoDocResponse></s:Body></s:Envelope>`
				default:
					t.Fatal("unexpected request")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			result, err := client.ReceivedRegistry(context.Background(), credentials, "76000000-0", 33, 123, "RCD")
			if err != nil || result.Code == nil || *result.Code != 8 || calls != 3 {
				t.Fatalf("result %+v err=%v", result, err)
			}
		})
	}
}
