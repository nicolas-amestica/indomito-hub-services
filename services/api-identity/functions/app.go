package functions

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"os"
	"sync"
)

type App struct {
	DDB         awsddb.Client
	SSM         *ssm.Client
	Table       string
	SecretParam string
}

var once sync.Once
var instance *App
var appErr error

func GetApp(ctx context.Context) (*App, error) {
	once.Do(func() {
		c := bootstrap.LoadConfig()
		a, e := bootstrap.LoadAWSConfig(ctx, c)
		if e != nil {
			appErr = e
			return
		}
		table := os.Getenv("USERS_TABLE_NAME")
		param := os.Getenv("JWT_SIGNING_SECRET_PARAM")
		if table == "" || param == "" {
			appErr = fmt.Errorf("configuracion de identidad incompleta")
			return
		}
		instance = &App{DDB: awsddb.New(a), SSM: ssm.NewFromConfig(a), Table: table, SecretParam: param}
	})
	return instance, appErr
}
