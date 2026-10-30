package sii

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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
			if request.Method != http.MethodGet || request.URL.Host != "apicert.sii.cl" {
				t.Fatalf("unexpected seed request: %s %s", request.Method, request.URL)
			}
			return response("application/xml", `<RESPUESTA><SEMILLA>1234567890</SEMILLA></RESPUESTA>`), nil
		case 2:
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil || request.Method != http.MethodPost || request.URL.Host != "apicert.sii.cl" || !strings.Contains(string(body), "Signature") {
				t.Fatal("invalid token request")
			}
			return response("application/xml", `<RESPUESTA><TOKEN>TOKEN-DE-PRUEBA</TOKEN></RESPUESTA>`), nil
		case 3:
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil || request.URL.Host != "pangal.sii.cl" || request.Header.Get("Cookie") != "TOKEN=TOKEN-DE-PRUEBA" || !strings.Contains(string(body), "EnvioBOLETA") || !strings.Contains(request.Header.Get("Content-Type"), "multipart/form-data") {
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
