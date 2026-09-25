package functions

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"regexp"
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

	// Path es el path del endpoint en la notacion del API Gateway: espanol,
	// plural, kebab-case, sin tildes y con los parametros entre llaves
	// (`/favoritos/{id-favorito}`), segun
	// docs/standards/global/api-design.md.
	Path string
}

// pathParamPattern captura un parametro de path en la notacion del API Gateway
// (`{id-favorito}`), para traducirlo a la de Echo (`:id-favorito`).
var pathParamPattern = regexp.MustCompile(`\{([^{}]+)\}`)

// LocalPath devuelve el path en la notacion de Echo, que marca los parametros
// con dos puntos en vez de llaves.
//
// La traduccion vive aca y no en cada endpoint por la misma razon que [Route]:
// el path se declara una sola vez, en la notacion que despliega serverless.ts,
// y el servidor local lo deriva. Escribir las dos formas a mano seria
// exactamente la desincronizacion que este tipo evita.
func (r Route) LocalPath() string {
	return pathParamPattern.ReplaceAllString(r.Path, ":$1")
}

// Register registra el handler de esta ruta en el servidor local de Echo. Es
// lo que llama la funcion Register de cada endpoint, para no repetir el metodo
// ni el path en el paquete del handler.
func (r Route) Register(e *echo.Echo, handler echo.HandlerFunc) {
	e.Add(r.Method, r.LocalPath(), handler)
}

// FavoriteIDParam es el nombre del parametro de path que identifica al
// favorito. Los handlers de actualizar y eliminar lo leen con este nombre,
// tanto del evento de API Gateway como del contexto de Echo, porque
// [Route.LocalPath] conserva el nombre y solo cambia la notacion.
const FavoriteIDParam = "id-cotizacion"

// Rutas de los cuatro endpoints del servicio. Son la unica declaracion Go de su
// superficie HTTP: ver [Route].
//
// Ninguna es publica. El identificador del usuario sale del contexto del
// authorizer y no del cuerpo ni de una cabecera (Requirement 19.8), asi que
// mientras el authorizer compartido no exista estas rutas solo existen en el
// servidor local (Requirement 19.4).
var (
	// ListFavoritesRoute lista los favoritos del usuario autenticado. El scope
	// llega como parametro de consulta (`?scope=programa`), que no forma parte
	// del path.
	ListFavoritesRoute = Route{Method: http.MethodGet, Path: "/cotizaciones"}

	// CreateFavoriteRoute crea un favorito con el contenido del programa en
	// curso.
	CreateFavoriteRoute = Route{Method: http.MethodPost, Path: "/cotizaciones"}

	// UpdateFavoriteRoute reemplaza el contenido de un favorito existente del
	// usuario autenticado.
	UpdateFavoriteRoute = Route{Method: http.MethodPut, Path: "/cotizaciones/{" + FavoriteIDParam + "}"}

	// DeleteFavoriteRoute elimina un favorito del usuario autenticado.
	DeleteFavoriteRoute = Route{Method: http.MethodDelete, Path: "/cotizaciones/{" + FavoriteIDParam + "}"}
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
// se ejercita de punta a punta contra este servidor (Requirement 19.5). Para
// este servicio no es una comodidad sino la unica forma de ejercitarlo: sus
// cuatro endpoints estan bloqueados para despliegue por el Requirement 19.4.
// No participa del despliegue: en AWS cada endpoint entra por su propio binario
// Lambda, y el puente entre el evento de API Gateway y el handler lo pone
// libs/lambdautil.EchoAdapter, que ademas inyecta la identidad de desarrollo en
// el mismo lugar del contexto donde el authorizer la publica en AWS.
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
		zap.String("listFavoritesPath", ListFavoritesRoute.LocalPath()),
		zap.String("createFavoritePath", CreateFavoriteRoute.LocalPath()),
		zap.String("updateFavoritePath", UpdateFavoriteRoute.LocalPath()),
		zap.String("deleteFavoritePath", DeleteFavoriteRoute.LocalPath()),
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
