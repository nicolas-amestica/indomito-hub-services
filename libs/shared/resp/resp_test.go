package resp

import (
	"encoding/json"
	"net/http"
	"testing"

	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

// TestSuccess_WrapsDataInEnvelope verifica que Success produzca el estado
// pedido, el header JSON y el cuerpo con el envelope { data }.
func TestSuccess_WrapsDataInEnvelope(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	got := Success(http.StatusCreated, payload{Name: "gira"})

	if got.StatusCode != http.StatusCreated {
		t.Errorf("StatusCode = %d, want %d", got.StatusCode, http.StatusCreated)
	}
	if got.Headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got.Headers["Content-Type"])
	}

	var decoded struct {
		Data payload `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.Body), &decoded); err != nil {
		t.Fatalf("no se pudo decodificar el cuerpo: %v", err)
	}
	if decoded.Data.Name != "gira" {
		t.Errorf("data.name = %q, want gira", decoded.Data.Name)
	}
}

// TestError_UsesRequestIDAsTraceID verifica que el traceId de la respuesta
// de error sea exactamente el requestId recibido, y que el estado HTTP y el
// code provengan del AppError.
func TestError_UsesRequestIDAsTraceID(t *testing.T) {
	tests := []struct {
		name       string
		requestID  string
		err        *apperr.AppError
		wantStatus int
		wantCode   string
	}{
		{
			name:       "error de validacion",
			requestID:  "01JQZ8REQUEST",
			err:        apperr.Validation("el escenario 2 declara un precio no positivo"),
			wantStatus: http.StatusBadRequest,
			wantCode:   apperr.CodeValidationError,
		},
		{
			name:       "recurso no encontrado",
			requestID:  "01JQZ8OTRO",
			err:        apperr.NotFound("favorito no encontrado"),
			wantStatus: http.StatusNotFound,
			wantCode:   apperr.CodeResourceNotFound,
		},
		{
			name:       "upstream sin snapshot",
			requestID:  "01JQZ8UPSTREAM",
			err:        apperr.UpstreamServiceError("la fuente de tasas no respondio"),
			wantStatus: http.StatusBadGateway,
			wantCode:   apperr.CodeUpstreamServiceError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Error(tt.requestID, tt.err)

			if got.StatusCode != tt.wantStatus {
				t.Errorf("StatusCode = %d, want %d", got.StatusCode, tt.wantStatus)
			}

			var decoded struct {
				Code    string         `json:"code"`
				Message string         `json:"message"`
				Details map[string]any `json:"details"`
				TraceID string         `json:"traceId"`
			}
			if err := json.Unmarshal([]byte(got.Body), &decoded); err != nil {
				t.Fatalf("no se pudo decodificar el cuerpo: %v", err)
			}

			if decoded.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", decoded.Code, tt.wantCode)
			}
			if decoded.TraceID != tt.requestID {
				t.Errorf("traceId = %q, want %q", decoded.TraceID, tt.requestID)
			}
			if decoded.Message != tt.err.Message() {
				t.Errorf("message = %q, want %q", decoded.Message, tt.err.Message())
			}
		})
	}
}

// TestError_IncludesDetailsWhenPresent verifica que los detalles del
// AppError, cuando existen, viajen en el cuerpo de la respuesta.
func TestError_IncludesDetailsWhenPresent(t *testing.T) {
	err := apperr.Validation("valor fuera de rango").WithDetails(map[string]any{
		"field":    "scenarios[1].pricePerPassengerCLP",
		"received": float64(0),
	})

	got := Error("01JQZ8TRACE", err)

	var decoded struct {
		Details map[string]any `json:"details"`
	}
	if unmarshalErr := json.Unmarshal([]byte(got.Body), &decoded); unmarshalErr != nil {
		t.Fatalf("no se pudo decodificar el cuerpo: %v", unmarshalErr)
	}
	if decoded.Details["field"] != "scenarios[1].pricePerPassengerCLP" {
		t.Errorf("details.field = %v, want scenarios[1].pricePerPassengerCLP", decoded.Details["field"])
	}
}

// TestError_NilAppErrorFallsBackToInternal verifica que un nil no produzca
// una respuesta vacia o rota, sino un INTERNAL_ERROR generico.
func TestError_NilAppErrorFallsBackToInternal(t *testing.T) {
	got := Error("01JQZ8TRACE", nil)

	if got.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", got.StatusCode, http.StatusInternalServerError)
	}

	var decoded struct {
		Code    string `json:"code"`
		TraceID string `json:"traceId"`
	}
	if err := json.Unmarshal([]byte(got.Body), &decoded); err != nil {
		t.Fatalf("no se pudo decodificar el cuerpo: %v", err)
	}
	if decoded.Code != apperr.CodeInternalError {
		t.Errorf("code = %q, want %q", decoded.Code, apperr.CodeInternalError)
	}
	if decoded.TraceID != "01JQZ8TRACE" {
		t.Errorf("traceId = %q, want 01JQZ8TRACE", decoded.TraceID)
	}
}
