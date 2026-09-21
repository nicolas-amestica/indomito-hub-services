package functions

import (
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
)

// FavoritesTableNameEnv es la variable de entorno que trae el nombre de la
// tabla `favoritos`. Se declara aca y en el `requiredEnvironment` de
// service.config.json: el validador de despliegue falla si serverless.ts no la
// publica en provider.environment, de modo que una funcion nunca llega a AWS
// sin saber a que tabla apuntar.
//
// En AWS el valor sale del output FavoritosTableName del stack de DynamoDB
// (repo ind-hub-inf). En local sale de configs/.env.local de este servicio.
const FavoritesTableNameEnv = "FAVORITES_TABLE_NAME"

// Config es la configuracion propia de api-favorite, resuelta una vez por
// arranque en frio a partir del entorno.
//
// Es una sola tabla para los cuatro endpoints, y es la unica a la que el
// servicio tiene permiso de llegar: el Requirement 17.11 acota sus politicas
// IAM a `favoritos`. Los favoritos se guardan particionados por usuario
// (pk = USER#<userId>), asi que no hay nombre de tabla por scope ni por
// endpoint.
type Config struct {
	// FavoritesTableName es el nombre de la tabla `favoritos` en DynamoDB.
	FavoritesTableName string

	// FunctionName es el nombre de la funcion en ejecucion
	// (`fn-listar-favoritos-v1`, `fn-crear-favorito-v1`,
	// `fn-actualizar-favorito-v1`, `fn-eliminar-favorito-v1`). Lo publica
	// serverless.ts por funcion y viaja en cada linea de log.
	FunctionName string

	// Port es el puerto del servidor local de desarrollo. No se usa en AWS.
	//
	// Mientras el authorizer compartido no exista, ese servidor es la unica
	// forma de ejercitar este servicio de punta a punta (Requirement 19.5).
	Port string
}

// LoadConfig arma la configuracion del servicio a partir de la configuracion
// base que ya cargo bootstrap.
//
// El nombre de la tabla es obligatorio: sin el, los cuatro endpoints del
// servicio no pueden hacer nada util. bootstrap.GetRequiredEnv corta el proceso
// si falta, y eso es deliberado — un arranque en frio que falla es mas facil de
// diagnosticar que un endpoint que responde 500 en cada invocacion.
func LoadConfig(base bootstrap.Config) Config {
	return Config{
		FavoritesTableName: bootstrap.GetRequiredEnv(FavoritesTableNameEnv),
		FunctionName:       bootstrap.GetEnv("APP_FUNCTION_NAME", base.AppName),
		Port:               base.Port,
	}
}
