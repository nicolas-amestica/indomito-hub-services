package functions

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	"go.uber.org/zap"
)

// Route es la superficie HTTP de un endpoint: su metodo y su path.
//
// El README del repo advierte que el path declarado en serverless.ts (lo que se
// despliega en el API Gateway compartido) y el que registra el servidor local
// son dos declaraciones distintas y es facil desincronizarlas. Este tipo reduce
// el problema al minimo posible dentro de Go: cada endpoint tiene una sola
// constante, definida aca, y tanto el registro en Echo como cualquier otro uso
// del path la derivan de ella. La contraparte en TypeScript es el arreglo
// `endpoints` de serverless.ts, que es tambien una sola declaracion por
// endpoint; el test de consistencia de este paquete comprueba que ambas
// coincidan.
type Route struct {
	// Method es el metodo HTTP del endpoint.
	Method string

	// Path es el path del endpoint en la notacion del API Gateway: espanol,
	// plural, kebab-case, sin tildes, con `:accion` para una operacion que no
	// es CRUD y con los parametros entre llaves, segun
	// docs/standards/global/api-design.md.
	Path string
}

// pathParamPattern captura un parametro de path en la notacion del API Gateway
// (`{id-programa}`), para traducirlo a la de Echo (`:id-programa`).
var pathParamPattern = regexp.MustCompile(`\{([^{}]+)\}`)

// LocalPath devuelve el path en la notacion de Echo.
//
// Son dos traducciones, y las dos existen porque Echo usa los dos puntos para
// marcar un parametro mientras que el API Gateway usa llaves:
//
//  1. Los dos puntos literales del path se escapan con una barra invertida.
//     Un `:` sin escapar convierte todo lo que le sigue en un parametro, asi
//     que `/programas:presupuesto` registrado tal cual no seria una ruta
//     estatica: Echo la partiria en el nodo estatico `/programas` mas un
//     parametro llamado `presupuesto`, y ese parametro tambien haria coincidir
//     `/programasCualquierCosa`. Con `\:` (router.go de echo/v4 quita la barra
//     y trata el `:` como un caracter mas) la ruta queda estatica y solo
//     responde al path exacto.
//  2. Los parametros entre llaves pasan a la notacion de Echo. Este servicio no
//     tiene ninguno hoy, pero la traduccion se conserva para que agregar un
//     endpoint con parametro no obligue a reescribir esto.
//
// Queda una combinacion que Echo no sabe expresar: un parametro y una accion en
// el mismo segmento (`/programas/{id-programa}:presupuesto`). El nombre de un
// parametro de Echo se extiende hasta la siguiente barra y se come el `\:`, asi
// que esa forma necesitaria registrar la ruta de otra manera. Ningun endpoint
// del backend la usa; si alguno la necesita, el arreglo va aca y no en el
// endpoint.
//
// El orden importa: primero se escapan los dos puntos que ya venian en el path
// —en la notacion del API Gateway todos son literales, porque los parametros
// van entre llaves— y solo despues se introducen los de los parametros, que si
// deben quedar sin escapar.
//
// La traduccion vive aca y no en cada endpoint por la misma razon que [Route]:
// el path se declara una sola vez, en la notacion que despliega serverless.ts,
// y el servidor local lo deriva. Escribir las dos formas a mano seria
// exactamente la desincronizacion que este tipo evita.
func (r Route) LocalPath() string {
	escaped := strings.ReplaceAll(r.Path, ":", `\:`)

	return pathParamPattern.ReplaceAllString(escaped, ":$1")
}

// Register registra el handler de esta ruta en el servidor local de Echo. Es lo
// que llama la funcion Register del endpoint, para no repetir el metodo ni el
// path en el paquete del handler.
func (r Route) Register(e *echo.Echo, handler echo.HandlerFunc) {
	e.Add(r.Method, r.LocalPath(), handler)
}

// GenerateBudgetRoute es el unico endpoint del servicio (Requirement 17.1):
// recibe el programa con sus escenarios ya valorizados y devuelve el PDF de
// presupuesto.
//
// Es la unica declaracion Go de su superficie HTTP: ver [Route].
//
// El path usa `:presupuesto` y no un sustantivo mas del path porque generar el
// presupuesto no es una operacion CRUD sobre un recurso `programas`: no crea ni
// modifica nada, produce un documento. `api-design.md` reserva la notacion
// `:accion` para ese caso.
//
// No es publico. El endpoint genera un documento comercial e invoca compute
// facturable, asi que necesita saber quien llama, y mientras el authorizer
// compartido no exista solo existe en el servidor local (Requirement 19.4).
var GenerateBudgetRoute = Route{Method: http.MethodPost, Path: "/cotizaciones:presupuesto"}

// localShutdownTimeout es el margen que se le da al servidor local para cerrar
// las conexiones en curso tras un Ctrl+C.
const localShutdownTimeout = 10 * time.Second

// RegisterFunc es la firma de la funcion Register que expone
// functions/generar-presupuesto-v1/endpoint.go.
type RegisterFunc func(e *echo.Echo, app *App, log *zap.Logger)

// RunLocal levanta el servidor Echo de desarrollo con los endpoints indicados.
//
// Existe porque el authorizer compartido todavia no esta activo y el formulario
// se ejercita de punta a punta contra este servidor (Requirement 19.5). Para
// este servicio no es una comodidad sino la unica forma de ejercitarlo: su
// endpoint esta bloqueado para despliegue por el Requirement 19.4. No participa
// del despliegue: en AWS el endpoint entra por su propio binario Lambda, y el
// puente entre el evento de API Gateway y el handler lo pone
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
		zap.String("generateBudgetPath", GenerateBudgetRoute.LocalPath()),
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
