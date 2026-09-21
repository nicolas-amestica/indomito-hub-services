// Package functions concentra el arranque, la configuracion y las rutas del
// servicio api-program, que genera el PDF de presupuesto del programa.
//
// El handler vive en un subpaquete (functions/generar-presupuesto-v1), segun el
// paradigma endpoint-per-function del repo: el endpoint compila a su propio
// binario Lambda y solo arrastra el codigo que usa. Hoy el servicio tiene un
// solo endpoint (Requirement 17.1), pero la estructura es la misma que en los
// otros dos servicios del backend.
//
// El servicio no accede a DynamoDB. Recibe el programa y los montos por
// escenario ya calculados en el cuerpo de la peticion y solo los maqueta, asi
// que no importa libs/awsddb ni construye cliente del SDK: los Requirements
// 17.9 y 17.12 quedan implementados por construccion, aca y en serverless.ts.
//
// Depende de libs/domain/program para los tipos del programa que dibuja, y
// nunca de api-catalog ni de api-favorite: son modulos Go que despliegan por
// separado y el Requirement 17.6 acota las dependencias de codigo de cada
// servicio a las librerias compartidas del repo y a modulos externos.
package functions

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/logger"
)

// App agrupa las dependencias compartidas del servicio, que hoy son solo su
// configuracion.
//
// A diferencia de api-catalog y api-favorite, no lleva cliente de DynamoDB:
// este servicio no consulta ninguna tabla. Ese campo ausente es la mitad en Go
// del Requirement 17.9; la otra mitad es la ausencia de politicas IAM en
// serverless.ts.
//
// Se construye una sola vez por contenedor de Lambda, no por invocacion. El
// beneficio es menor que en un servicio con cliente del SDK — no hay handshake
// TLS que evitar — pero mantiene una sola forma de obtener la configuracion y
// el logger en los tres servicios del backend.
type App struct {
	// Config es la configuracion del servicio resuelta desde el entorno.
	Config Config

	// Stage es el ambiente de despliegue (dev, prd, o local cuando corre bajo
	// el servidor de desarrollo). Viaja en cada linea de log.
	Stage string
}

var (
	appOnce sync.Once
	appInst *App
)

// GetApp devuelve la instancia compartida del servicio, construyendola en la
// primera llamada.
//
// No resuelve la configuracion de AWS: sin cliente del SDK que construir,
// bootstrap.LoadAWSConfig solo agregaria I/O de resolucion de credenciales al
// arranque en frio, y ademas haria fallar el servidor local en una maquina sin
// perfil de AWS configurado — justo el entorno en que hoy se ejercita este
// servicio (Requirement 19.5).
//
// Conserva ctx y el error en la firma para que sea la misma que en los otros
// dos servicios, y para que el dia en que el arranque necesite I/O el cambio
// quede contenido en este cuerpo y no alcance a los llamadores. Hoy el error
// devuelto es siempre nil.
func GetApp(ctx context.Context) (*App, error) {
	appOnce.Do(func() {
		base := bootstrap.LoadConfig()

		appInst = &App{
			Config: LoadConfig(base),
			Stage:  base.AppStage,
		}
	})

	return appInst, nil
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
