package main

import (
	"context"
	"os"

	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/logger"
	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions"
	taxsettings "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/actualizar-configuracion-tributaria-v1"
	servicecatalog "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/catalogo-servicios"
	catalogs "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/obtener-catalogos-v1"
	gettaxsettings "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/obtener-configuracion-tributaria-v1"
	masteraccess "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/masteraccess"
	rates "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/obtener-tasas-cambio-v1"
)

func main() {
	log := logger.New("api-catalog-local", "startup", "local")

	if err := functions.RunLocal(context.Background(), masteraccess.Register, catalogs.Register, gettaxsettings.Register, taxsettings.Register, servicecatalog.Register, rates.Register); err != nil {
		log.Error("localServerError", zap.Error(err))
		// Sync puede devolver un error al cerrar stdout en algunas plataformas;
		// el error relevante ya quedó registrado y el proceso debe terminar.
		_ = log.Sync()
		os.Exit(1)
	}

	_ = log.Sync()
}
