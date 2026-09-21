package main

import (
	"context"
	"os"

	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/logger"
	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions"
	catalogs "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/obtener-catalogos-v1"
	rates "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/obtener-tasas-cambio-v1"
)

func main() {
	log := logger.New("api-catalog-local", "startup", "local")

	if err := functions.RunLocal(context.Background(), catalogs.Register, rates.Register); err != nil {
		log.Error("localServerError", zap.Error(err))
		// Sync puede devolver un error al cerrar stdout en algunas plataformas;
		// el error relevante ya quedó registrado y el proceso debe terminar.
		_ = log.Sync()
		os.Exit(1)
	}

	_ = log.Sync()
}
