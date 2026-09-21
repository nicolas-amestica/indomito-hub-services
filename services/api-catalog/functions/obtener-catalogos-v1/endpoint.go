// Package catalogs implementa GET /catalogos: los catálogos de plan, temporada
// y destino del formulario de programa, junto con los parámetros de política de
// la empresa (Requirement 16).
//
// El nombre del paquete no repite el del directorio (`obtener-catalogos-v1`,
// que nombra la función Lambda) porque un identificador Go no admite guiones.
// Se prefiere `catalogs` sobre una transliteración del directorio para que el
// llamador lea `catalogs.Register` en cmd/, que es donde se cablea.
//
// La lógica del endpoint está en fn-query-catalog.go. Este archivo se ocupa
// solo de la superficie HTTP: resolver las dependencias, elegir el estado y las
// cabeceras, y dejar una línea de log por invocación.
package catalogs

import (
	"context"
	"net/http"

	"github.com/aws/aws-lambda-go/events"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/logger"

	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions"
)

// Cabecera de caché de la respuesta (Requirement 16.10).
//
// Cinco minutos: son datos que cambian con frecuencia de meses, así que la
// caché del navegador elimina la petición en cada navegación al formulario sin
// que el cotizador llegue a ver un catálogo viejo. Va solo en la respuesta
// exitosa; cachear un error dejaría el formulario roto durante el mismo lapso.
const (
	cacheControlHeader = "Cache-Control"
	cacheControlValue  = "max-age=300"
)

// resolvedEvent es el evento de log de una invocación que resolvió los
// catálogos.
const resolvedEvent = "catalogsResolved"

// queryErrorEvent es el evento de log de una consulta que falló.
const queryErrorEvent = "catalogsQueryError"

// Register registra el endpoint en el servidor local de Echo, tomando el path y
// el método de functions.CatalogsRoute para no declararlos de nuevo acá.
//
// Sirve las peticiones locales con la App que recibe, en vez de resolverla por
// invocación: es la misma instancia que construyó RunLocal, y pasarla explícita
// deja visible de dónde salen el cliente de DynamoDB y el nombre de la tabla.
//
// log queda sin usar a propósito. Es el logger del servidor, etiquetado con el
// requestId "local", y cada petición necesita el suyo con el requestId de su
// propio evento: eso lo da app.Logger dentro de handle. El parámetro está en la
// firma porque functions.RegisterFunc es uniforme para todos los endpoints.
func Register(e *echo.Echo, app *functions.App, log *zap.Logger) {
	functions.CatalogsRoute.Register(e, lambdautil.EchoAdapter(
		func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
			return handle(ctx, app, req)
		},
	))
}

// Handle procesa la invocación de la función Lambda. Es lo que recibe
// cmd/fn-obtener-catalogos-v1.
func Handle(
	ctx context.Context,
	req events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}

	return handle(ctx, app, req)
}

// handle resuelve la petición con las dependencias ya construidas.
//
// Recibe la App en vez de pedirla, y eso es lo que permite ejercitarlo en los
// tests con un doble del cliente de DynamoDB, sin tocar AWS ni el entorno.
func handle(
	ctx context.Context,
	app *functions.App,
	req events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error) {
	log := app.Logger(req.RequestContext.RequestID)
	defer func() {
		// Sync sobre stdout devuelve error en algunas plataformas y no hay nada
		// que hacer al respecto al cerrar la invocación.
		_ = log.Sync()
	}()

	response, err := QueryCatalog(ctx, app.DDB, app.Config.CatalogsTableName, log)
	if err != nil {
		logger.WarnOrError(log, err, queryErrorEvent)
		return lambdautil.ErrorResponse(req, err)
	}

	// Banderas y no valores: alcanzan para diagnosticar el caso en que un
	// cotizador reporta los campos de margen en blanco, que siempre es un ítem
	// SETTINGS ausente o mal cargado. Los valores de margin son política
	// comercial y no aportan al diagnóstico, así que no se registran.
	log.Info(resolvedEvent,
		zap.Int("optionCount", response.OptionCount()),
		zap.Bool("hasMargin", response.Settings.Margin != nil),
		zap.Bool("hasScenarioOffsets", len(response.Settings.ScenarioOffsets) > 0),
	)

	return lambdautil.SuccessResponseWithHeaders(http.StatusOK, response, map[string]string{
		cacheControlHeader: cacheControlValue,
	})
}
