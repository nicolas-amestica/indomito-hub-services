package functions

import (
	"github.com/labstack/echo/v4"
	"regexp"
)

// Route mantiene método y ruta compartidos entre Lambda y desarrollo local.
type Route struct {
	Method string
	Path   string
}

var routeParam = regexp.MustCompile(`\{([^{}]+)\}`)

// Register adapta parámetros de Gateway a la convención de Echo.
func (r Route) Register(e *echo.Echo, handler echo.HandlerFunc) {
	e.Add(r.Method, routeParam.ReplaceAllString(r.Path, ":$1"), handler)
}

// ListBalanceRoute consulta el catálogo documental completo por páginas.
var ListBalanceRoute = Route{Method: "GET", Path: "/analitica/giras"}

// GetBalanceRoute consulta un presupuesto y su posición financiera agregada.
var GetBalanceRoute = Route{Method: "GET", Path: "/analitica/giras/{id-viaje}"}

// SaveBalanceRoute aplica un comando versionado de planificación.
var SaveBalanceRoute = Route{Method: "PUT", Path: "/analitica/giras/{id-viaje}/presupuesto"}
