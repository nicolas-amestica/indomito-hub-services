package functions

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	"go.uber.org/zap"
)

// Route es la superficie HTTP de un endpoint: su metodo y su path.
//
// El README del repo advierte que el path declarado en serverless.ts (lo que
// se despliega en el API Gateway compartido) y el que registra el servidor
// local son dos declaraciones distintas y es facil desincronizarlas. Este tipo
// reduce el problema al minimo posible dentro de Go: cada endpoint tiene una
// sola constante, definida aca, y tanto el registro en Echo como cualquier otro
// uso del path la derivan de ella. La contraparte en TypeScript es el arreglo
// `endpoints` de serverless.ts, que es tambien una sola declaracion por
// endpoint; el test de consistencia de este paquete comprueba que ambas
// coincidan.
type Route struct {
	// Method es el metodo HTTP del endpoint.
	Method string

	// Path es el path del endpoint: espanol, plural, kebab-case y sin tildes,
	// segun docs/standards/global/api-design.md.
	Path string
}

// Register registra el handler de esta ruta en el servidor local de Echo. Es
// lo que llama la funcion Register de cada endpoint, para no repetir el metodo
// ni el path en el paquete del handler.
func (r Route) Register(e *echo.Echo, handler echo.HandlerFunc) {
	e.Add(r.Method, r.Path, handler)
}

// Rutas de los dos endpoints del servicio. Son la unica declaracion Go de su
// superficie HTTP: ver [Route].
var (
	// CatalogsRoute expone los catalogos del formulario de programa (planes,
	// temporadas, destinos) y los parametros de politica de la empresa.
	CatalogsRoute = Route{Method: http.MethodGet, Path: "/catalogos"}

	// ExchangeRatesRoute expone las tasas de cambio de USD y BRL a CLP, con
	// respaldo desde el ultimo snapshot cuando la fuente externa no responde.
	ExchangeRatesRoute = Route{Method: http.MethodGet, Path: "/tasas-cambio"}
)

// localShutdownTimeout es el margen que se le da al servidor local para cerrar
// las conexiones en curso tras un Ctrl+C.
const localShutdownTimeout = 10 * time.Second

// RegisterFunc es la firma de la funcion Register que expone cada
// functions/<endpoint>/endpoint.go.
type RegisterFunc func(e *echo.Echo, app *App, log *zap.Logger)

// RunLocal levanta el servidor Echo de desarrollo con los endpoints indicados.
//
// Existe porque el authorizer compartido todavia no esta activo y el formulario
// se ejercita de punta a punta contra este servidor (Requirement 19.5). No
// participa del despliegue: en AWS cada endpoint entra por su propio binario
// Lambda, y el puente entre el evento de API Gateway y el handler lo pone
// libs/lambdautil.EchoAdapter.
func RunLocal(ctx context.Context, registers ...RegisterFunc) error {
	app, err := GetApp(ctx)
	if err != nil {
		return err
	}

	log := app.Logger("local")
	defer func() {
		// Sync sobre stdout devuelve error en algunas plataformas y no hay nada
		// que hacer al respecto durante el apagado.
		_ = log.Sync()
	}()

	e := echo.New()
	e.HideBanner = true
	e.Use(echomiddleware.Recover())
	e.Use(echomiddleware.RequestID())
	e.Use(echomiddleware.CORSWithConfig(echomiddleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodOptions,
		},
		AllowHeaders: []string{
			echo.HeaderOrigin,
			echo.HeaderContentType,
			echo.HeaderAccept,
			echo.HeaderAuthorization,
		},
	}))

	for _, register := range registers {
		register(e, app, log)
	}

	go func() {
		if err := e.Start(":" + app.Config.Port); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("localServerStartError", zap.Error(err))
		}
	}()

	log.Info("localServerStarted",
		zap.String("port", app.Config.Port),
		zap.String("catalogsPath", CatalogsRoute.Path),
		zap.String("exchangeRatesPath", ExchangeRatesRoute.Path),
	)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutdownCtx, cancel := context.WithTimeout(context.Background(), localShutdownTimeout)
	defer cancel()

	if err := e.Shutdown(shutdownCtx); err != nil {
		return err
	}

	log.Info("localServerStopped")

	return nil
}
