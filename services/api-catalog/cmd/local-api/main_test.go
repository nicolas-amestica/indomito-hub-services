package main

import (
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions"
	catalogs "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/obtener-catalogos-v1"
	rates "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/obtener-tasas-cambio-v1"
)

func TestLocalServerRegistersBothRoutes(t *testing.T) {
	e := echo.New()
	app := &functions.App{}

	catalogs.Register(e, app, zap.NewNop())
	rates.Register(e, app, zap.NewNop())

	registered := make(map[string]bool)
	for _, route := range e.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	for _, route := range []functions.Route{functions.CatalogsRoute, functions.ExchangeRatesRoute} {
		key := route.Method + " " + route.Path
		if !registered[key] {
			t.Errorf("el servidor local no registró %s", key)
		}
		if route.Method != http.MethodGet {
			t.Errorf("%s usa el método %s, se esperaba GET", route.Path, route.Method)
		}
	}
}
