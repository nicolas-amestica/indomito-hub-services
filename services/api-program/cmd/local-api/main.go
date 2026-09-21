package main

import (
	"context"
	"os"

	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/logger"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions"
	budget "ind-hub-api-gox-sls-pri-gh/services/api-program/functions/generar-presupuesto-v1"
)

func main() {
	log := logger.New("api-program-local", "startup", "local")

	if err := functions.RunLocal(context.Background(), budget.Register); err != nil {
		log.Error("localServerError", zap.Error(err))
		_ = log.Sync()
		os.Exit(1)
	}

	_ = log.Sync()
}
