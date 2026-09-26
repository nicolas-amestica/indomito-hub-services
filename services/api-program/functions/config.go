package functions

import (
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
)

const ProgramsTableNameEnv = "PROGRAMS_TABLE_NAME"

// Config es la configuracion propia de api-program, resuelta una vez por
// arranque en frio a partir del entorno.
//
// No declara nombre de tabla, y la ausencia es deliberada: es el Requirement
// 17.9 implementado por construccion. El servicio recibe el programa y los
// montos por escenario ya calculados en el cuerpo de
// POST /programas:presupuesto y solo maqueta el PDF, asi que no tiene a que
// tabla apuntar. Su serverless.ts tampoco declara politicas IAM, de modo que su
// rol no alcanza ninguna tabla (Requirement 17.12): no es que el servicio
// decida no consultar la base, es que no puede.
//
// Si en algun momento este struct gana un nombre de tabla, eso ya no es un
// ajuste de configuracion sino un cambio de contrato del servicio: obligaria a
// revisar los Requirements 17.9 y 17.12 antes de escribirlo.
type Config struct {
	ProgramsTableName string
	// FunctionName es el nombre de la funcion en ejecucion
	// (`fn-generar-presupuesto-v1`). Lo publica serverless.ts por funcion y
	// viaja en cada linea de log.
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
// No usa bootstrap.GetRequiredEnv: ninguna variable propia del servicio es
// obligatoria. Las tres que si lo son (APP_NAME, APP_STAGE y APP_REGION) las
// resuelve bootstrap.LoadConfig con valores por defecto, y el
// `requiredEnvironment` de service.config.json es el que comprueba que
// serverless.ts las publique antes de un despliegue.
func LoadConfig(base bootstrap.Config) Config {
	return Config{
		ProgramsTableName: bootstrap.GetRequiredEnv(ProgramsTableNameEnv),
		FunctionName:      bootstrap.GetEnv("APP_FUNCTION_NAME", base.AppName),
		Port:              base.Port,
	}
}
