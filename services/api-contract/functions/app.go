package functions

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"go.uber.org/zap"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/libs/logger"
)

type App struct {
	Config          Config
	DDB             awsddb.Client
	DDBTransactions *dynamodb.Client
	Documents       DocumentStore
	Stage           string
}

var once sync.Once
var instance *App
var appErr error

func GetApp(ctx context.Context) (*App, error) {
	once.Do(func() {
		base := bootstrap.LoadConfig()
		cfg, err := bootstrap.LoadAWSConfig(ctx, base)
		if err != nil {
			appErr = fmt.Errorf("cargar configuracion AWS: %w", err)
			return
		}
		config := LoadConfig(base)
		ddb := awsddb.New(cfg)
		instance = &App{Config: config, DDB: ddb, DDBTransactions: ddb, Documents: newS3DocumentStore(cfg, config.DocumentsBucketName), Stage: base.AppStage}
	})
	return instance, appErr
}
func (a *App) Logger(requestID string) *zap.Logger {
	return logger.New(a.Config.FunctionName, requestID, a.Stage)
}
