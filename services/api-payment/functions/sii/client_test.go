package sii

import (
	"context"
	"crypto/sha1" // #nosec G505 -- el formato XMLDSIG del SII exige SHA-1.
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func TestClientSubmitsSignedBoletaThroughCertificationFlow(t *testing.T) {
	for _, environment := range []Environment{EnvironmentCertification, EnvironmentProduction} {
		t.Run(string(environment), func(t *testing.T) {
			now := time.Date(2026, 10, 29, 21, 30, 0, 0, time.UTC)
			credentials := testCredentials(t, now)
			credentials.Environment = environment
			seedURL, tokenURL, uploadURL := endpoints(environment)
			document, err := BuildDocument(credentials, Sale{RequestID: "01K8R8TV0J6V3N3V5W9Z6B8A7C", Folio: 7, Amount: 287500, IssueDate: now, Description: "Cuota de gira de estudios"}, now)
			if err != nil {
				t.Fatal(err)
			}
			step := 0
			client := Client{Now: func() time.Time { return now }, HTTP: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				step++
				response := func(contentType, body string) *http.Response {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body))}
				}
				switch step {
				case 1:
					if request.Method != http.MethodGet || request.URL.String() != seedURL || request.Header.Get("Accept") != "application/xml" {
						t.Fatalf("unexpected seed request: %s %s", request.Method, request.URL)
					}
					return response("application/xml", `<RESPUESTA><RESP_BODY><SEMILLA>1234567890</SEMILLA></RESP_BODY></RESPUESTA>`), nil
				case 2:
					body, readErr := io.ReadAll(request.Body)
					contents := string(body)
					if readErr != nil || request.Method != http.MethodPost || request.URL.String() != tokenURL || request.Header.Get("Content-Type") != "application/xml" ||
						!strings.Contains(contents, "<getToken>") || !strings.Contains(contents, "<Signature") ||
						!strings.Contains(contents, "<RSAKeyValue>") || !strings.Contains(contents, "<X509Certificate>") {
						t.Fatal("invalid token request")
					}
					return response("application/xml", `<RESPUESTA><RESP_BODY><TOKEN>TOKEN-DE-PRUEBA</TOKEN></RESP_BODY></RESPUESTA>`), nil
				case 3:
					body, readErr := io.ReadAll(request.Body)
					if readErr != nil || request.Method != http.MethodPost || request.URL.String() != uploadURL || request.Header.Get("Accept") != "application/json" || request.Header.Get("Cookie") != "TOKEN=TOKEN-DE-PRUEBA" || !strings.Contains(string(body), "EnvioBOLETA") || !strings.Contains(request.Header.Get("Content-Type"), "multipart/form-data") {
						t.Fatal("invalid upload request")
					}
					return response("application/json", `{"trackid":123456}`), nil
				default:
					t.Fatalf("unexpected request %d", step)
					return nil, nil
				}
			})}

			submission, err := client.Submit(context.Background(), credentials, document)
			if err != nil {
				t.Fatalf("submit: %v", err)
			}
			if submission.TrackID != "123456" || submission.Status != "SUBMITTED" || step != 3 {
				t.Fatalf("unexpected submission: %+v after %d requests", submission, step)
			}
		})
	}
}

func TestClientRejectsSuccessfulHTTPUploadWithoutTrackID(t *testing.T) {
	client := Client{HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"error":"rechazado","mensaje":"archivo invalido"}`))}, nil
	})}
	credentials := Credentials{Config: Config{Environment: EnvironmentCertification, SenderRUT: "76000000-0"}, IssuerRUT: "76000000-0"}
	_, err := client.upload(context.Background(), credentials, "TOKEN", []byte("invalid"))
	if err == nil {
		t.Fatal("expected rejected upload")
	}
}

func TestClientQueriesProductionBoletaStatusThroughREST(t *testing.T) {
	for _, environment := range []Environment{EnvironmentCertification, EnvironmentProduction} {
		t.Run(string(environment), func(t *testing.T) {
			now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
			credentials := testCredentials(t, now)
			credentials.Environment = environment
			apiHost := "api.sii.cl"
			if environment == EnvironmentCertification {
				apiHost = "apicert.sii.cl"
			}
			credentials.IssuerRUT = "77654796-4"
			step := 0
			client := Client{Now: func() time.Time { return now }, HTTP: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				step++
				response := func(contentType, body string) *http.Response {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body))}
				}
				switch step {
				case 1:
					if request.Method != http.MethodGet || request.URL.Host != apiHost || request.URL.Path != "/recursos/v1/boleta.electronica.semilla" {
						t.Fatalf("unexpected seed request: %s %s", request.Method, request.URL)
					}
					return response("application/xml", `<RESPUESTA><RESP_BODY><SEMILLA>1234567890</SEMILLA></RESP_BODY></RESPUESTA>`), nil
				case 2:
					body, readErr := io.ReadAll(request.Body)
					if readErr != nil || request.Method != http.MethodPost || request.URL.Host != apiHost || request.URL.Path != "/recursos/v1/boleta.electronica.token" || !strings.Contains(string(body), "<Signature") {
						t.Fatalf("unexpected token request: %s %s", request.Method, request.URL)
					}
					return response("application/xml", `<RESPUESTA><RESP_BODY><TOKEN>TOKEN-BOLETA</TOKEN></RESP_BODY></RESPUESTA>`), nil
				case 3:
					if request.Method != http.MethodGet || request.URL.Host != apiHost || request.URL.Path != "/recursos/v1/boleta.electronica.envio/77654796-4-24477625070" || request.Header.Get("Cookie") != "TOKEN=TOKEN-BOLETA" || request.Header.Get("Accept") != "application/json" {
						t.Fatalf("unexpected status request: %s %s", request.Method, request.URL)
					}
					return response("application/json", `{"estado":"EPR","estadistica":[{"tipo":39,"informados":1,"aceptados":0,"rechazados":0,"reparos":1}],"detalle_rep_rech":[{"tipo":39,"folio":16004,"estado":"RPR","descripcion":"Dte Aceptado con reparos","error":[{"seccion":"DET","codigo":200,"descripcion":"Reparo de prueba","detalle":"detalle tecnico"}]}]}`), nil
				default:
					t.Fatalf("unexpected request %d", step)
					return nil, nil
				}
			})}

			status, err := client.QuerySubmission(context.Background(), credentials, "24477625070")
			if err != nil {
				t.Fatalf("query submission: %v", err)
			}
			if step != 3 || status.State != "EPR" || status.Informed != 1 || status.Accepted != 0 || status.Rejected != 0 || status.Repairs != 1 || !status.Approved() {
				t.Fatalf("unexpected status: %+v after %d requests", status, step)
			}
			if !strings.Contains(status.Detail, "Dte Aceptado con reparos") || !strings.Contains(status.Detail, "200: Reparo de prueba") {
				t.Fatalf("missing SII diagnostic: %q", status.Detail)
			}
		})
	}
}

func TestClientRejectsInvalidBoletaTrackIDBeforeCallingSII(t *testing.T) {
	client := Client{Now: time.Now, HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("SII must not be called for an invalid Track ID")
		return nil, nil
	})}
	credentials := Credentials{Config: Config{Environment: EnvironmentProduction}, IssuerRUT: "77654796-4"}
	if _, err := client.QuerySubmission(context.Background(), credentials, "not-a-track-id"); err == nil {
		t.Fatal("expected invalid Track ID error")
	}
}

func TestSubmissionStatusRequiresProcessedEnvelopeWithoutRejections(t *testing.T) {
	accepted := SubmissionStatus{State: "EPR", Informed: 5, Accepted: 4, Repairs: 1}
	if !accepted.Approved() {
		t.Fatal("processed envelope with accepted documents and repairs should be approved")
	}
	rejected := SubmissionStatus{State: "EPR", Informed: 5, Accepted: 4, Rejected: 1}
	if rejected.Approved() {
		t.Fatal("envelope with rejected documents must not be approved")
	}
	incomplete := SubmissionStatus{State: "EPR"}
	if incomplete.Approved() {
		t.Fatal("processed envelope without document statistics must remain pending")
	}
}

func TestBuildEnvelopeAcceptsLatin1DTE(t *testing.T) {
	now := time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)
	credentials := testCredentials(t, now)
	credentials.IssuerActivity = "Actividades de agencias de viaje y operador turístico"
	document, err := BuildDocument(credentials, Sale{RequestID: "CASO-1", Folio: 1, Amount: 28900, IssueDate: now, Description: "Cuota de gira de estudios"}, now)
	if err != nil {
		t.Fatalf("build document: %v", err)
	}
	envelope, err := buildEnvelope(credentials, document.XML, now)
	if err != nil {
		t.Fatalf("build envelope with Latin-1 DTE: %v", err)
	}
	decoded, err := decodeLatin1(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(decoded), "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n") {
		t.Fatal("envelope XML declaration must be the first node")
	}
	lines := strings.SplitN(string(decoded), "\n", 3)
	if len(lines) < 2 || !strings.HasPrefix(lines[1], `<EnvioBOLETA `) || !strings.Contains(lines[1], `EnvioBOLETA_v11.xsd`) {
		t.Fatalf("SII schema declaration must be on line 2: %q", lines)
	}
	parsed := etree.NewDocument()
	if err = parsed.ReadFromBytes(decoded); err != nil {
		t.Fatal(err)
	}
	children := parsed.Root().ChildElements()
	if len(children) != 2 || children[0].Tag != "SetDTE" || children[1].Tag != "Signature" || children[0].FindElement("Signature") != nil {
		t.Fatal("envelope signature must be a sibling of SetDTE")
	}
	if parsed.FindElement("//Signature/KeyInfo/KeyValue/RSAKeyValue") == nil {
		t.Fatal("envelope signature is missing RSAKeyValue")
	}
}

func TestBuildEnvelopeSetPreservesCertificationOrder(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	credentials := testCredentials(t, now)
	documents := make([]Document, 0, 5)
	for index := 1; index <= 5; index++ {
		document, err := BuildDocument(credentials, Sale{RequestID: "01M68000000000000000000000", Folio: int64(index), Amount: 1000, IssueDate: now, Reference: fmt.Sprintf("CASO-%d", index), Items: []SaleItem{{Description: fmt.Sprintf("item %d", index), Quantity: 1, UnitPrice: 1000}}}, now)
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	envelope, err := buildEnvelopeSet(credentials, documents, now)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeLatin1(envelope)
	if err != nil {
		t.Fatal(err)
	}
	parsed := etree.NewDocument()
	if err = parsed.ReadFromBytes(decoded); err != nil {
		t.Fatal(err)
	}
	if count := parsed.FindElement("//SubTotDTE/NroDTE"); count == nil || count.Text() != "5" {
		t.Fatalf("unexpected DTE count: %v", count)
	}
	dtes := parsed.FindElements("//SetDTE/DTE")
	if len(dtes) != 5 {
		t.Fatalf("unexpected DTE count: %d", len(dtes))
	}
	for index, dte := range dtes {
		if reference := dte.FindElement("Documento/Referencia/RazonRef"); reference == nil || reference.Text() != fmt.Sprintf("CASO-%d", index+1) {
			t.Fatalf("unexpected reference at %d: %v", index, reference)
		}
		document := dte.FindElement("Documento")
		digestValue := dte.FindElement("Signature/SignedInfo/Reference/DigestValue")
		canonical, canonicalErr := dsig.MakeC14N10RecCanonicalizer().Canonicalize(document)
		if canonicalErr != nil {
			t.Fatal(canonicalErr)
		}
		digest := sha1.Sum(canonical) // #nosec G401 -- el formato XMLDSIG del SII exige SHA-1.
		if digestValue == nil || digestValue.Text() != base64.StdEncoding.EncodeToString(digest[:]) {
			t.Fatalf("embedded DTE %d digest changed inside EnvioBOLETA", index+1)
		}
	}
	set := parsed.FindElement("//SetDTE")
	digestValue := parsed.FindElement("//Signature/SignedInfo/Reference/DigestValue")
	canonical, err := dsig.MakeC14N10RecCanonicalizer().Canonicalize(set)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha1.Sum(canonical) // #nosec G401 -- el formato XMLDSIG del SII exige SHA-1.
	if digestValue == nil || digestValue.Text() != base64.StdEncoding.EncodeToString(digest[:]) {
		t.Fatal("serialized certification envelope changed after its digest was signed")
	}
	signatures := parsed.FindElements("//Signature")
	if len(signatures) != 6 {
		t.Fatalf("unexpected signature count: %d", len(signatures))
	}
	for _, signature := range signatures {
		assertXMLSignatureBase64Wrapped(t, signature)
	}
}
