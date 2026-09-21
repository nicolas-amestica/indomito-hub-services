package main

import (
	"context"
	"os"

	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/logger"
	"ind-hub-api-gox-sls-pri-gh/services/api-favorite/functions"
	update "ind-hub-api-gox-sls-pri-gh/services/api-favorite/functions/actualizar-favorito-v1"
	create "ind-hub-api-gox-sls-pri-gh/services/api-favorite/functions/crear-favorito-v1"
	deleteFavorite "ind-hub-api-gox-sls-pri-gh/services/api-favorite/functions/eliminar-favorito-v1"
	list "ind-hub-api-gox-sls-pri-gh/services/api-favorite/functions/listar-favoritos-v1"
)

func main() {
	log := logger.New("api-favorite-local", "startup", "local")

	if err := functions.RunLocal(
		context.Background(),
		list.Register,
		create.Register,
		update.Register,
		deleteFavorite.Register,
	); err != nil {
		log.Error("localServerError", zap.Error(err))
		_ = log.Sync()
		os.Exit(1)
	}

	_ = log.Sync()
}
