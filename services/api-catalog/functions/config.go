package functions

import (
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
)

// CatalogsTableNameEnv es la variable de entorno que trae el nombre de la
// tabla `catalogos`. Se declara aca y en el `requiredEnvironment` de
// service.config.json: el validador de despliegue falla si serverless.ts no la
// publica en provider.environment, de modo que una funcion nunca llega a AWS
// sin saber a que tabla apuntar.
//
// En AWS el valor sale del output CatalogosTableName del stack de DynamoDB
// (repo ind-hub-inf). En local sale de configs/.env.local de este servicio.
const CatalogsTableNameEnv = "CATALOGS_TABLE_NAME"

// BCCHAPITokenEnv autentica las consultas REST a la BDE del Banco Central.
// Es opcional para que el servicio pueda recurrir a las fuentes de respaldo
// mientras el ambiente todavía no tenga configurado el token.
const BCCHAPITokenEnv = "BCCH_API_TOKEN"

// Config es la configuracion propia de api-catalog, resuelta una vez por
// arranque en frio a partir del entorno.
//
// La tabla `catalogos` guarda tres cosas distintas bajo claves de particion
// separadas: los catalogos y los parametros de politica (pk = CATALOG) y el
// snapshot de respaldo de tasas de cambio (pk = RATES). Por eso el servicio
// declara un unico nombre de tabla y no uno por endpoint.
type Config struct {
	// CatalogsTableName es el nombre de la tabla `catalogos` en DynamoDB.
	CatalogsTableName string

	// BCCHAPIToken es el token de la API BDE. Nunca se registra en logs.
	BCCHAPIToken string

	// FunctionName es el nombre de la funcion en ejecucion
	// (`fn-obtener-catalogos-v1`, `fn-obtener-tasas-cambio-v1`). Lo publica
	// serverless.ts por funcion y viaja en cada linea de log.
	FunctionName string

	// Port es el puerto del servidor local de desarrollo. No se usa en AWS.
	Port string
}

// LoadConfig arma la configuracion del servicio a partir de la configuracion
// base que ya cargo bootstrap.
//
// El nombre de la tabla es obligatorio: sin el, los dos endpoints del servicio
// no pueden hacer nada util. bootstrap.GetRequiredEnv corta el proceso si
// falta, y eso es deliberado — un arranque en frio que falla es mas facil de
// diagnosticar que un endpoint que responde 500 en cada invocacion.
func LoadConfig(base bootstrap.Config) Config {
	return Config{
		CatalogsTableName: bootstrap.GetRequiredEnv(CatalogsTableNameEnv),
		BCCHAPIToken:      bootstrap.GetEnv(BCCHAPITokenEnv, ""),
		FunctionName:      bootstrap.GetEnv("APP_FUNCTION_NAME", base.AppName),
		Port:              base.Port,
	}
}
