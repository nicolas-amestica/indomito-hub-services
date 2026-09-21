// Package functions concentra el arranque, la configuracion y las rutas del
// servicio api-catalog, que expone los catalogos del formulario de programa y
// las tasas de cambio.
//
// Los handlers viven en subpaquetes, uno por endpoint
// (functions/obtener-catalogos-v1, functions/obtener-tasas-cambio-v1), segun el
// paradigma endpoint-per-function del repo: cada endpoint compila a su propio
// binario Lambda y solo arrastra el codigo que usa.
package functions

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/libs/logger"
)

// App agrupa las dependencias compartidas del servicio: su configuracion y el
// cliente de DynamoDB.
//
// Se construye una sola vez por contenedor de Lambda, no por invocacion: el
// cliente del SDK es seguro para uso concurrente y reutilizarlo entre
// invocaciones evita repetir el handshake TLS en cada llamada.
type App struct {
	// Config es la configuracion del servicio resuelta desde el entorno.
	Config Config

	// DDB es la superficie de DynamoDB que los handlers reciben. Se declara
	// como interfaz para que los tests puedan sustituirla sin tocar AWS.
	DDB awsddb.Client

	// Stage es el ambiente de despliegue (dev, prd, o local cuando corre bajo
	// el servidor de desarrollo). Viaja en cada linea de log.
	Stage string
}

var (
	appOnce sync.Once
	appInst *App
	appErr  error
)

// GetApp devuelve la instancia compartida del servicio, construyendola en la
// primera llamada.
//
// Recibe ctx porque la carga de la configuracion de AWS hace I/O (resolucion
// de credenciales), y asi el timeout de la invocacion la cancela en vez de
// esperarla indefinidamente.
func GetApp(ctx context.Context) (*App, error) {
	appOnce.Do(func() {
		base := bootstrap.LoadConfig()

		awsCfg, err := bootstrap.LoadAWSConfig(ctx, base)
		if err != nil {
			appErr = fmt.Errorf("no se pudo cargar la configuracion de AWS: %w", err)
			return
		}

		appInst = &App{
			Config: LoadConfig(base),
			DDB:    awsddb.New(awsCfg),
			Stage:  base.AppStage,
		}
	})

	return appInst, appErr
}

// Logger construye el logger de una invocacion, ya etiquetado con el nombre de
// la funcion, el requestId del evento de API Gateway y el stage.
//
// Es el unico camino para obtener un logger dentro de un handler: libs/logger
// prohibe fmt.Println y este metodo evita que cada endpoint repita de donde
// salen los tres campos obligatorios.
func (a *App) Logger(requestID string) *zap.Logger {
	return logger.New(a.Config.FunctionName, requestID, a.Stage)
}
