package functions

import (
	"context"
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"ind-hub-api-gox-sls-pri-gh/bootstrap"
)

var (
	appOnce   sync.Once
	appInst   *bootstrap.App
	appErr    error
	appLogger *zap.Logger
)

// GetApp obtiene o inicializa la aplicación compartida del servicio.
func GetApp(ctx context.Context) (*bootstrap.App, error) {
	appOnce.Do(func() {
		appInst = bootstrap.MustNew(ctx)

		log, err := InitLogger()
		if err != nil {
			appErr = err
			return
		}
		appLogger = log
	})

	return appInst, appErr
}

// GetLogger obtiene el logger estructurado usado por el servicio.
func GetLogger() *zap.Logger {
	return appLogger
}

// IsLambda returns true when running inside AWS Lambda.
func IsLambda() bool {
	return os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != ""
}

// InitLogger builds the zap logger for the service.
func InitLogger() (*zap.Logger, error) {
	if IsLambda() {
		cfg := zap.NewProductionConfig()
		cfg.EncoderConfig.TimeKey = "timestamp"
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		return cfg.Build()
	}

	cfg := zap.NewDevelopmentConfig()
	cfg.Encoding = "console"
	cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	cfg.EncoderConfig.TimeKey = "time"
	cfg.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("15:04:05")
	cfg.EncoderConfig.CallerKey = ""
	cfg.EncoderConfig.StacktraceKey = ""
	cfg.EncoderConfig.LineEnding = "\n"
	return cfg.Build()
}
