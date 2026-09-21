package lambdautil

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

// TestSuccessResponse_WrapsDataInEnvelope verifica que la respuesta exitosa use
// el envelope { data } y que el segundo valor sea nil, para que un handler pueda
// cerrar con un solo return.
func TestSuccessResponse_WrapsDataInEnvelope(t *testing.T) {
	response, err := SuccessResponse(http.StatusCreated, map[string]string{"id": "01JQZ8FAV"})
	if err != nil {
		t.Fatalf("SuccessResponse devolvio error: %v", err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Errorf("StatusCode = %d, want %d", response.StatusCode, http.StatusCreated)
	}

	var decoded struct {
		Data map[string]string `json:"data"`
	}
	if unmarshalErr := json.Unmarshal([]byte(response.Body), &decoded); unmarshalErr != nil {
		t.Fatalf("no se pudo decodificar el cuerpo: %v", unmarshalErr)
	}
	if decoded.Data["id"] != "01JQZ8FAV" {
		t.Errorf("data.id = %q, want 01JQZ8FAV", decoded.Data["id"])
	}
}

// TestSuccessResponseWithHeaders_AddsHeaders verifica que las cabeceras propias
// del endpoint, como el Cache-Control de los catalogos, convivan con las del
// envelope.
func TestSuccessResponseWithHeaders_AddsHeaders(t *testing.T) {
	response, err := SuccessResponseWithHeaders(
		http.StatusOK,
		map[string]int{"optionCount": 12},
		map[string]string{"Cache-Control": "max-age=300"},
	)
	if err != nil {
		t.Fatalf("SuccessResponseWithHeaders devolvio error: %v", err)
	}
	if response.Headers["Cache-Control"] != "max-age=300" {
		t.Errorf("Cache-Control = %q, want max-age=300", response.Headers["Cache-Control"])
	}
	if response.Headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", response.Headers["Content-Type"])
	}
}

// TestErrorResponse_UsesRequestIDAsTraceID verifica que el traceId de la
// respuesta de error sea el requestId del evento, que es lo que permite cruzarla
// con los access logs del stage.
func TestErrorResponse_UsesRequestIDAsTraceID(t *testing.T) {
	req := events.APIGatewayV2HTTPRequest{}
	req.RequestContext.RequestID = "01JQZ8REQUEST"

	response, err := ErrorResponse(req, apperr.NotFound("El favorito no existe"))
	if err != nil {
		t.Fatalf("ErrorResponse devolvio error: %v", err)
	}
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", response.StatusCode, http.StatusNotFound)
	}

	decoded := decodeErrorBody(t, response.Body)
	if decoded.Code != apperr.CodeResourceNotFound {
		t.Errorf("code = %q, want %q", decoded.Code, apperr.CodeResourceNotFound)
	}
	if decoded.TraceID != "01JQZ8REQUEST" {
		t.Errorf("traceId = %q, want 01JQZ8REQUEST", decoded.TraceID)
	}
}

// TestErrorResponse_NormalizesUnknownError verifica que un error cualquiera se
// traduzca a INTERNAL_ERROR sin filtrar su mensaje crudo al cliente.
func TestErrorResponse_NormalizesUnknownError(t *testing.T) {
	req := events.APIGatewayV2HTTPRequest{}
	req.RequestContext.RequestID = "01JQZ8REQUEST"

	response, err := ErrorResponse(req, errors.New("dynamodb: ProvisionedThroughputExceeded en tabla favoritos"))
	if err != nil {
		t.Fatalf("ErrorResponse devolvio error: %v", err)
	}
	if response.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}

	decoded := decodeErrorBody(t, response.Body)
	if decoded.Code != apperr.CodeInternalError {
		t.Errorf("code = %q, want %q", decoded.Code, apperr.CodeInternalError)
	}
	if decoded.Message != "Ocurrio un error inesperado" {
		t.Errorf("message = %q, want el mensaje generico", decoded.Message)
	}
}

type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
	TraceID string         `json:"traceId"`
}

func decodeErrorBody(t *testing.T, body string) errorBody {
	t.Helper()

	var decoded errorBody
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("no se pudo decodificar el cuerpo: %v", err)
	}
	return decoded
}
