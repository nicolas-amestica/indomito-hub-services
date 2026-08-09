package bootstrap

import (
	"log"
	"os"
)

// GetRequiredEnv obtiene una variable de entorno obligatoria o termina el proceso.
func GetRequiredEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("missing required environment variable: %s", key)
	}

	return value
}

// GetEnv obtiene una variable de entorno con valor por defecto.
func GetEnv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
