package functions

import (
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
)

// Config contiene la configuración del servicio authorizer.
type Config struct {
	JWTSecret string
}

// LoadConfig carga la configuración desde variables de entorno.
func LoadConfig(_ *bootstrap.App) Config {
	return Config{
		JWTSecret: bootstrap.GetRequiredEnv("JWT_SECRET"),
	}
}
