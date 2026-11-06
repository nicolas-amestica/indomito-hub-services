package sii

import (
	"context"
	"crypto/sha1" // #nosec G505 -- el formato XMLDSIG del SII exige SHA-1.
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

func TestSubmitConsumptionPreservesCertificationDTEFlow(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	credentials := testCredentials(t, now)
	step := 0
	client := Client{Now: func() time.Time { return now }, HTTP: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		step++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if request.Method != http.MethodPost || request.URL.Host != "maullin.sii.cl" {
			t.Fatalf("unexpected DTE request: %s %s", request.Method, request.URL)
		}
		var response string
		switch step {
		case 1:
			if request.URL.Path != "/DTEWS/CrSeed.jws" || !strings.Contains(string(body), "getSeed") {
				t.Fatal("invalid SOAP seed request")
			}
			response = `<RESPUESTA><SEMILLA>1234567890</SEMILLA></RESPUESTA>`
		case 2:
			if request.URL.Path != "/DTEWS/GetTokenFromSeed.jws" || !strings.Contains(string(body), "&lt;Signature") {
				t.Fatal("invalid SOAP token request")
			}
			response = `<RESPUESTA><TOKEN>RCOF-TOKEN</TOKEN></RESPUESTA>`
		case 3:
			if request.URL.Path != "/cgi_dte/UPL/DTEUpload" || request.Header.Get("Cookie") != "TOKEN=RCOF-TOKEN" || !strings.Contains(string(body), "ConsumoFolios") {
				t.Fatal("invalid RCOF upload")
			}
			response = `<RECEPCIONDTE><STATUS>0</STATUS><TRACKID>123456</TRACKID></RECEPCIONDTE>`
		default:
			t.Fatalf("unexpected request %d", step)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}
	submission, err := client.SubmitConsumption(context.Background(), credentials, []byte(`<ConsumoFolios/>`))
	if err != nil || submission.TrackID != "123456" || step != 3 {
		t.Fatalf("unexpected RCOF submission: %+v, steps=%d, error=%v", submission, step, err)
	}
}

func TestBuildConsumptionMatchesFiveDocumentCertificationSet(t *testing.T) {
	now := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
	credentials := testCredentials(t, now)
	credentials.FolioFrom, credentials.FolioTo = 102, 106
	contents, err := BuildConsumption(credentials, ConsumptionResume{Date: now, Sequence: 1, NetAmount: 43831, VATAmount: 8329, ExemptAmount: 2000, TotalAmount: 54160, FolioFrom: 102, FolioTo: 106}, now)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeLatin1(contents)
	if err != nil {
		t.Fatal(err)
	}
	document := etree.NewDocument()
	if err = document.ReadFromBytes(decoded); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"//TipoDocumento": "39", "//MntNeto": "43831", "//MntIva": "8329", "//TasaIVA": "19", "//MntExento": "2000", "//MntTotal": "54160", "//FoliosEmitidos": "5", "//FoliosAnulados": "0", "//FoliosUtilizados": "5", "//RangoUtilizados/Inicial": "102", "//RangoUtilizados/Final": "106", "//SecEnvio": "1"}
	for path, expected := range want {
		element := document.FindElement(path)
		if element == nil || element.Text() != expected {
			t.Fatalf("%s = %v, want %s", path, element, expected)
		}
	}
	signed := document.FindElement("//DocumentoConsumoFolios")
	digestValue := document.FindElement("//Signature/SignedInfo/Reference/DigestValue")
	canonical, err := dsig.MakeC14N10RecCanonicalizer().Canonicalize(signed)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha1.Sum(canonical) // #nosec G401 -- el formato XMLDSIG del SII exige SHA-1.
	if digestValue == nil || digestValue.Text() != base64.StdEncoding.EncodeToString(digest[:]) {
		t.Fatal("serialized RCOF changed after its digest was signed")
	}
}

func TestBuildConsumptionRejectsTotalsOutsideCAF(t *testing.T) {
	now := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
	credentials := testCredentials(t, now)
	credentials.FolioFrom, credentials.FolioTo = 102, 106
	_, err := BuildConsumption(credentials, ConsumptionResume{Date: now, Sequence: 1, NetAmount: 1, TotalAmount: 2, FolioFrom: 101, FolioTo: 106}, now)
	if err == nil {
		t.Fatal("expected invalid consumption resume")
	}
}
