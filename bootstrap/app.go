package bootstrap

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// Config agrupa la configuración necesaria para ejecutar el paquete.
type Config struct {
	AppName               string
	AppStage              string
	AppRegion             string
	AppMode               string
	LogLevel              string
	Port                  string
	AppProfile            string
	AssumeRoleARN         string
	AssumeRoleSessionName string
}

// App representa la estructura de inicialización de la aplicación.
type App struct {
	Config    Config
	AWSConfig aws.Config
}

// MustNew inicializa la aplicación con su configuración AWS.
func MustNew(ctx context.Context) *App {
	cfg := LoadConfig()

	awsConfig, err := LoadAWSConfig(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}

	return &App{
		Config:    cfg,
		AWSConfig: awsConfig,
	}
}

// LoadConfig carga la configuración desde variables de entorno.
func LoadConfig() Config {
	return Config{
		AppName:               GetEnv("APP_NAME", "indomito-api"),
		AppStage:              GetEnv("APP_STAGE", GetEnv("STAGE", "localhost")),
		AppRegion:             GetEnv("APP_REGION", GetEnv("AWS_REGION", "us-east-1")),
		AppMode:               GetEnv("APP_MODE", "lambda"),
		LogLevel:              GetEnv("LOG_LEVEL", "info"),
		Port:                  GetEnv("PORT", "8080"),
		AppProfile:            GetEnv("AWS_PROFILE", ""),
		AssumeRoleARN:         GetEnv("AWS_ROLE_ARN", ""),
		AssumeRoleSessionName: GetEnv("AWS_ROLE_SESSION_NAME", ""),
	}
}
