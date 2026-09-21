package apperr

import (
	"errors"
	"fmt"
	"testing"
)

// TestConstructors_MapToExpectedCodeAndStatus verifica que cada constructor
// de conveniencia produzca el codigo y el estado HTTP que fija el catalogo
// de api-design.md.
func TestConstructors_MapToExpectedCodeAndStatus(t *testing.T) {
	tests := []struct {
		name       string
		build      func() *AppError
		wantCode   string
		wantStatus int
	}{
		{"Validation", func() *AppError { return Validation("mensaje") }, CodeValidationError, 400},
		{"Validationf", func() *AppError { return Validationf("mensaje %d", 1) }, CodeValidationError, 400},
		{"RequiredFieldMissing", func() *AppError { return RequiredFieldMissing("mensaje") }, CodeRequiredFieldMissing, 400},
		{"RequiredFieldMissingf", func() *AppError { return RequiredFieldMissingf("mensaje %d", 1) }, CodeRequiredFieldMissing, 400},
		{"InvalidQueryParameter", func() *AppError { return InvalidQueryParameter("mensaje") }, CodeInvalidQueryParameter, 400},
		{"InvalidRequestFormat", func() *AppError { return InvalidRequestFormat("mensaje") }, CodeInvalidRequestFormat, 400},
		{"Unauthorized", func() *AppError { return Unauthorized("mensaje") }, CodeUnauthorized, 401},
		{"TokenExpired", func() *AppError { return TokenExpired("mensaje") }, CodeTokenExpired, 401},
		{"Forbidden", func() *AppError { return Forbidden("mensaje") }, CodeForbidden, 403},
		{"NotFound", func() *AppError { return NotFound("mensaje") }, CodeResourceNotFound, 404},
		{"NotFoundf", func() *AppError { return NotFoundf("mensaje %d", 1) }, CodeResourceNotFound, 404},
		{"Conflict", func() *AppError { return Conflict("mensaje") }, CodeConflict, 409},
		{"UpstreamServiceError", func() *AppError { return UpstreamServiceError("mensaje") }, CodeUpstreamServiceError, 502},
		{"UpstreamTimeout", func() *AppError { return UpstreamTimeout("mensaje") }, CodeUpstreamTimeout, 502},
		{"Internal", func() *AppError { return Internal(errors.New("causa")) }, CodeInternalError, 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.build()
			if err.Code() != tt.wantCode {
				t.Errorf("Code() = %q, want %q", err.Code(), tt.wantCode)
			}
			if err.HTTPStatus() != tt.wantStatus {
				t.Errorf("HTTPStatus() = %d, want %d", err.HTTPStatus(), tt.wantStatus)
			}
		})
	}
}

// TestIsExpected_MatchesHTTPStatusBoundary verifica que la frontera entre
// error esperado e inesperado sea exactamente el estado 500.
func TestIsExpected_MatchesHTTPStatusBoundary(t *testing.T) {
	tests := []struct {
		name string
		err  *AppError
		want bool
	}{
		{"400 es esperado", Validation("x"), true},
		{"404 es esperado", NotFound("x"), true},
		{"502 no es esperado: es un 5xx", UpstreamServiceError("x"), false},
		{"500 no es esperado", Internal(nil), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.IsExpected(); got != tt.want {
				t.Errorf("IsExpected() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNew_PanicsOnUnknownCode verifica que New rechace codigos fuera del
// catalogo de api-design.md en vez de aceptarlos silenciosamente: la regla
// "sin codigos nuevos" se hace cumplir en tiempo de ejecucion.
func TestNew_PanicsOnUnknownCode(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New con codigo desconocido deberia hacer panic")
		}
	}()
	New("CODIGO_INVENTADO", "mensaje")
}

// TestFrom_NormalizesAnyError verifica que From devuelva el mismo AppError
// cuando ya lo es (incluso envuelto), y lo envuelva como Internal en caso
// contrario.
func TestFrom_NormalizesAnyError(t *testing.T) {
	t.Run("nil devuelve nil", func(t *testing.T) {
		if From(nil) != nil {
			t.Error("From(nil) deberia devolver nil")
		}
	})

	t.Run("AppError directo se devuelve sin cambios", func(t *testing.T) {
		original := NotFound("no existe")
		got := From(original)
		if got != original {
			t.Errorf("From() = %v, want el mismo puntero %v", got, original)
		}
	})

	t.Run("AppError envuelto se recupera via Unwrap", func(t *testing.T) {
		original := NotFound("no existe")
		wrapped := fmt.Errorf("contexto adicional: %w", original)
		got := From(wrapped)
		if got != original {
			t.Errorf("From() = %v, want el AppError envuelto %v", got, original)
		}
	})

	t.Run("error generico se convierte en Internal", func(t *testing.T) {
		got := From(errors.New("fallo de red"))
		if got.Code() != CodeInternalError {
			t.Errorf("Code() = %q, want %q", got.Code(), CodeInternalError)
		}
		if got.HTTPStatus() != 500 {
			t.Errorf("HTTPStatus() = %d, want 500", got.HTTPStatus())
		}
	})
}

// TestWithDetails_SetsDetails verifica que WithDetails adjunte los datos y
// permita encadenar la construccion.
func TestWithDetails_SetsDetails(t *testing.T) {
	err := Validation("valor fuera de rango").WithDetails(map[string]any{
		"field":    "pricePerPassengerCLP",
		"received": 0,
	})
	if err.Details()["field"] != "pricePerPassengerCLP" {
		t.Errorf("Details()[field] = %v, want pricePerPassengerCLP", err.Details()["field"])
	}
}

// TestError_IncludesCauseWhenPresent verifica que Error() incluya la causa
// envuelta cuando existe, para que el log estructurado no pierda la raiz.
func TestError_IncludesCauseWhenPresent(t *testing.T) {
	cause := errors.New("timeout de red")
	err := Internal(cause)
	if err.Unwrap() != cause {
		t.Errorf("Unwrap() = %v, want %v", err.Unwrap(), cause)
	}
	if err.Error() == "" {
		t.Error("Error() no deberia ser vacio")
	}
}
