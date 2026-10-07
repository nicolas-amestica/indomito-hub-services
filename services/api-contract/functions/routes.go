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
const AmendmentIDParam = "id-anexo"
const SignedDocumentIDParam = "id-documento-firmado"

var CreateRoute = Route{http.MethodPost, "/contratos"}
var ListRoute = Route{http.MethodGet, "/contratos"}
var GetRoute = Route{http.MethodGet, "/contratos/{" + ContractIDParam + "}"}
var UpdateRoute = Route{http.MethodPut, "/contratos/{" + ContractIDParam + "}"}
var PDFRoute = Route{http.MethodPost, "/contratos:pdf"}
var ApprovedPDFRoute = Route{http.MethodGet, "/contratos/{" + ContractIDParam + "}/pdf"}
var ConfigurationRoute = Route{http.MethodGet, "/contratos:configuracion"}
var CreateAmendmentRoute = Route{http.MethodPost, "/contratos/{" + ContractIDParam + "}/anexos"}
var ListAmendmentsRoute = Route{http.MethodGet, "/contratos/{" + ContractIDParam + "}/anexos"}
var ApproveAmendmentRoute = Route{http.MethodPost, "/contratos/{" + ContractIDParam + "}/anexos/{" + AmendmentIDParam + "}/aprobacion"}
var ApprovedAmendmentPDFRoute = Route{http.MethodGet, "/contratos/{" + ContractIDParam + "}/anexos/{" + AmendmentIDParam + "}/pdf"}
var PrepareSignedDocumentRoute = Route{http.MethodPost, "/contratos/{" + ContractIDParam + "}/documentos-firmados:carga"}
var FinalizeSignedDocumentRoute = Route{http.MethodPost, "/contratos/{" + ContractIDParam + "}/documentos-firmados:confirmar"}
var ListSignedDocumentsRoute = Route{http.MethodGet, "/contratos/{" + ContractIDParam + "}/documentos-firmados"}
var SignedDocumentPDFRoute = Route{http.MethodGet, "/contratos/{" + ContractIDParam + "}/documento-firmado/pdf"}
var SignedDocumentVersionPDFRoute = Route{http.MethodGet, "/contratos/{" + ContractIDParam + "}/documentos-firmados/{" + SignedDocumentIDParam + "}/pdf"}

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
