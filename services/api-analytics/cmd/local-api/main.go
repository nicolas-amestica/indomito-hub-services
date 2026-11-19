package main

import (
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
	save "ind-hub-api-gox-sls-pri-gh/services/api-analytics/functions/guardar-balance-v1"
	list "ind-hub-api-gox-sls-pri-gh/services/api-analytics/functions/listar-balance-v1"
	get "ind-hub-api-gox-sls-pri-gh/services/api-analytics/functions/obtener-balance-v1"
	"os"
)

func main() {
	log, err := zap.NewDevelopment()
	if err != nil {
		os.Exit(1)
	}
	e := echo.New()
	list.Register(e, log)
	get.Register(e, log)
	save.Register(e, log)
	if err = e.Start("127.0.0.1:8098"); err != nil {
		log.Error("Servidor local detenido", zap.Error(err))
		os.Exit(1)
	}
}
