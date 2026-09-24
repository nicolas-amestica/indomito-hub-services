package functions

import (
	"context"
	"net/http"
	"regexp"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

type Route struct {
	Method string
	Path   string
}

var pathParamPattern = regexp.MustCompile(`\{([^{}]+)\}`)

func (r Route) LocalPath() string                         { return pathParamPattern.ReplaceAllString(r.Path, ":$1") }
func (r Route) Register(e *echo.Echo, h echo.HandlerFunc) { e.Add(r.Method, r.LocalPath(), h) }

const ContractIDParam = "id-contrato"

var CreateRoute = Route{http.MethodPost, "/contratos"}
var ListRoute = Route{http.MethodGet, "/contratos"}
var GetRoute = Route{http.MethodGet, "/contratos/{" + ContractIDParam + "}"}
var UpdateRoute = Route{http.MethodPut, "/contratos/{" + ContractIDParam + "}"}
var PDFRoute = Route{http.MethodPost, "/contratos:pdf"}

type RegisterFunc func(*echo.Echo, *App, *zap.Logger)

func RunLocal(ctx context.Context, registers ...RegisterFunc) error {
	app, err := GetApp(ctx)
	if err != nil {
		return err
	}
	e := echo.New()
	for _, register := range registers {
		register(e, app, app.Logger("local"))
	}
	return e.Start(":" + app.Config.Port)
}
