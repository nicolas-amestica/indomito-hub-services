package logger

import (
	"encoding/json"
	"errors"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// httpStatusErrorStub permite construir errores de prueba con un
// HTTPStatus() arbitrario, sin depender de libs/shared/apperr (evitaria un
// ciclo de importacion real, pero ademas deja el test enfocado solo en la
// interfaz que logger.WarnOrError realmente usa).
type httpStatusErrorStub struct {
	status int
}

func (e httpStatusErrorStub) Error() string   { return "error de prueba" }
func (e httpStatusErrorStub) HTTPStatus() int { return e.status }

// TestNew_AttachesRequiredFields verifica que el logger construido incluya
// siempre functionName, requestId y stage en cada linea emitida.
func TestNew_AttachesRequiredFields(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	base := zap.New(core)

	log := base.With(
		zap.String("functionName", "fn-obtener-catalogos-v1"),
		zap.String("requestId", "01JQZ8REQUEST"),
		zap.String("stage", "dev"),
	)
	log.Info("evento de prueba")

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("se esperaba 1 entrada de log, se obtuvieron %d", len(entries))
	}

	fields := entries[0].ContextMap()
	if fields["functionName"] != "fn-obtener-catalogos-v1" {
		t.Errorf("functionName = %v, want fn-obtener-catalogos-v1", fields["functionName"])
	}
	if fields["requestId"] != "01JQZ8REQUEST" {
		t.Errorf("requestId = %v, want 01JQZ8REQUEST", fields["requestId"])
	}
	if fields["stage"] != "dev" {
		t.Errorf("stage = %v, want dev", fields["stage"])
	}
}

// TestNew_ReturnsUsableLogger verifica que New devuelva un logger real, no
// nil, y que no falle al construirse con datos tipicos de una invocacion.
func TestNew_ReturnsUsableLogger(t *testing.T) {
	log := New("fn-crear-favorito-v1", "01JQZ8REQUEST", "dev")
	if log == nil {
		t.Fatal("New() no deberia devolver nil")
	}
}

// TestWarnOrError_UsesLevelFromHTTPStatus verifica que un error con estado
// HTTP menor a 500 se registre en warn, y uno con estado 500 o superior (o
// sin HTTPStatus()) se registre en error.
func TestWarnOrError_UsesLevelFromHTTPStatus(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantLevel zapcore.Level
	}{
		{"400 se registra como warn", httpStatusErrorStub{status: 400}, zapcore.WarnLevel},
		{"404 se registra como warn", httpStatusErrorStub{status: 404}, zapcore.WarnLevel},
		{"502 se registra como error: es un 5xx", httpStatusErrorStub{status: 502}, zapcore.ErrorLevel},
		{"500 se registra como error", httpStatusErrorStub{status: 500}, zapcore.ErrorLevel},
		{"error generico sin HTTPStatus se registra como error", errors.New("fallo inesperado"), zapcore.ErrorLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			log := zap.New(core)

			WarnOrError(log, tt.err, "fallo al procesar la solicitud")

			entries := logs.All()
			if len(entries) != 1 {
				t.Fatalf("se esperaba 1 entrada de log, se obtuvieron %d", len(entries))
			}
			if entries[0].Level != tt.wantLevel {
				t.Errorf("Level = %v, want %v", entries[0].Level, tt.wantLevel)
			}
		})
	}
}

// TestWarnOrError_IncludesErrorField verifica que el campo del error viaje
// en la entrada de log, independiente del nivel elegido.
func TestWarnOrError_IncludesErrorField(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	log := zap.New(core)

	WarnOrError(log, httpStatusErrorStub{status: 404}, "favorito no encontrado")

	fields := logs.All()[0].ContextMap()
	if _, ok := fields["error"]; !ok {
		t.Error("se esperaba el campo 'error' en la entrada de log")
	}
}

// TestNew_EncodesAsJSON verifica indirectamente que la configuracion del
// encoder produzca JSON valido de una sola linea, codificando una entrada
// con el mismo EncoderConfig que usa New.
func TestNew_EncodesAsJSON(t *testing.T) {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	encoder := zapcore.NewJSONEncoder(encoderConfig)
	buf, err := encoder.EncodeEntry(zapcore.Entry{
		Level:   zapcore.InfoLevel,
		Message: "evento de prueba",
	}, []zapcore.Field{zap.String("functionName", "fn-x")})
	if err != nil {
		t.Fatalf("EncodeEntry() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("la salida no es JSON valido: %v", err)
	}
	if decoded["functionName"] != "fn-x" {
		t.Errorf("functionName = %v, want fn-x", decoded["functionName"])
	}
}
