package main

import (
	"context"
	"os"

	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/logger"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions"
	update "ind-hub-api-gox-sls-pri-gh/services/api-program/functions/actualizar-cotizacion-v1"
	create "ind-hub-api-gox-sls-pri-gh/services/api-program/functions/crear-cotizacion-v1"
	deleteQuotation "ind-hub-api-gox-sls-pri-gh/services/api-program/functions/eliminar-cotizacion-v1"
	budget "ind-hub-api-gox-sls-pri-gh/services/api-program/functions/generar-presupuesto-v1"
	list "ind-hub-api-gox-sls-pri-gh/services/api-program/functions/listar-cotizaciones-v1"
)

func main() {
	log := logger.New("api-program-local", "startup", "local")

	if err := functions.RunLocal(
		context.Background(),
		list.Register,
		create.Register,
		update.Register,
		deleteQuotation.Register,
		budget.Register,
	); err != nil {
		log.Error("localServerError", zap.Error(err))
		_ = log.Sync()
		os.Exit(1)
	}

	_ = log.Sync()
}
