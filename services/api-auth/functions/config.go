package functions

import (
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
)

// Config holds the service configuration.
type Config struct {
	Port string
}

// LoadConfig loads configuration from environment variables.
func LoadConfig(_ *bootstrap.App) Config {
	return Config{
		Port: bootstrap.GetEnv("PORT", "8080"),
	}
}
