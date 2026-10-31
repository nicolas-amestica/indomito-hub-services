package sii

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func TestClientSubmitsSignedBoletaThroughCertificationFlow(t *testing.T) {
	now := time.Date(2026, 10, 29, 21, 30, 0, 0, time.UTC)
	credentials := testCredentials(t, now)
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
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil || request.Method != http.MethodPost || request.URL.Host != "maullin.sii.cl" || !strings.Contains(string(body), "getSeed") {
				t.Fatalf("unexpected seed request: %s %s", request.Method, request.URL)
			}
			return response("text/xml", `<Envelope><Body><getSeedResponse><getSeedReturn>&lt;RESPUESTA&gt;&lt;SEMILLA&gt;1234567890&lt;/SEMILLA&gt;&lt;/RESPUESTA&gt;</getSeedReturn></getSeedResponse></Body></Envelope>`), nil
		case 2:
			body, readErr := io.ReadAll(request.Body)
			contents := string(body)
			if readErr != nil || request.Method != http.MethodPost || request.URL.Host != "maullin.sii.cl" ||
				!strings.Contains(contents, "<ns1:getToken") || !strings.Contains(contents, "&lt;Signature") ||
				!strings.Contains(contents, "&lt;RSAKeyValue&gt;") || !strings.Contains(contents, "&lt;X509Certificate&gt;") {
				t.Fatal("invalid token request")
			}
			return response("text/xml", `<Envelope><Body><getTokenResponse><getTokenReturn>&lt;RESPUESTA&gt;&lt;TOKEN&gt;TOKEN-DE-PRUEBA&lt;/TOKEN&gt;&lt;/RESPUESTA&gt;</getTokenReturn></getTokenResponse></Body></Envelope>`), nil
		case 3:
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil || request.URL.Host != "maullin.sii.cl" || request.URL.Path != "/cgi_dte/UPL/DTEUpload" || request.Header.Get("Cookie") != "TOKEN=TOKEN-DE-PRUEBA" || !strings.Contains(string(body), "EnvioBOLETA") || !strings.Contains(request.Header.Get("Content-Type"), "multipart/form-data") {
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
	if !strings.HasPrefix(string(decoded), `<?xml version="1.0" encoding="ISO-8859-1"?>`) {
		t.Fatal("envelope XML declaration must be the first node")
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
