// Package logger construye el logger estructurado que usan las funciones
// Lambda del backend. Usa go.uber.org/zap con salida JSON de una sola linea
// a stdout, que es lo que CloudWatch Logs recolecta desde una funcion
// provided.al2023. Esta prohibido usar fmt.Println o log.Printf para logging
// en cualquier parte del backend Go: todo pasa por este paquete.
//
// Convencion de niveles: un error esperado (validacion, recurso no
// encontrado, snapshot de respaldo entregado) se registra con Warn. El nivel
// Error queda reservado para fallas inesperadas del sistema: un panic
// recuperado, una dependencia que no deberia fallar y falla, o cualquier
// error que no sea un *apperr.AppError con estado HTTP menor a 500. La
// funcion WarnOrError aplica esta regla a partir del estado HTTP del error.
package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// httpStatusError es la superficie minima que WarnOrError necesita del
// error de dominio para decidir el nivel de log. libs/shared/apperr.AppError
// la satisface sin que este paquete dependa de ese modulo, evitando un
// ciclo de importacion entre libs/logger y libs/shared/apperr.
type httpStatusError interface {
	error
	HTTPStatus() int
}

// New construye un logger de zap con salida JSON a stdout, preconfigurado
// con los tres campos que todo log de una invocacion Lambda debe llevar:
// el nombre de la funcion, el requestId del evento de API Gateway y el
// stage de despliegue (dev/prd). Cada handler llama a New una vez al inicio
// de la invocacion y usa el logger devuelto para todo el resto del log.
func New(functionName, requestID, stage string) *zap.Logger {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	config := zap.NewProductionConfig()
	config.EncoderConfig = encoderConfig
	config.DisableCaller = false

	base, err := config.Build()
	if err != nil {
		// La construccion del encoder de produccion no falla en la practica
		// con esta configuracion; si algo cambia y falla, un logger no-op es
		// preferible a un panic durante el arranque de la Lambda.
		base = zap.NewNop()
	}

	return base.With(
		zap.String("functionName", functionName),
		zap.String("requestId", requestID),
		zap.String("stage", stage),
	)
}

// WarnOrError registra msg en Warn si err es un error esperado (estado HTTP
// menor a 500) y en Error en caso contrario. Es la forma recomendada de
// loggear el error de un handler antes de construir la respuesta con
// libs/shared/resp.Error, para no repetir la decision de nivel en cada
// endpoint.
func WarnOrError(log *zap.Logger, err error, msg string, fields ...zap.Field) {
	allFields := append(fields, zap.Error(err))

	statusErr, ok := err.(httpStatusError)
	if ok && statusErr.HTTPStatus() < 500 {
		log.Warn(msg, allFields...)
		return
	}
	log.Error(msg, allFields...)
}
