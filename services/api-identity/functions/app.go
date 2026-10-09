package functions

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"os"
	"sync"
)

type App struct {
	DDB             awsddb.Client
	SSM             *ssm.Client
	Table           string
	SecretParam     string
	ResetEmailParam string
	secretOnce      sync.Once
	secret          string
	secretErr       error
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
		resetEmailParam := os.Getenv("PASSWORD_RESET_EMAIL_PARAM")
		if table == "" || param == "" || resetEmailParam == "" {
			appErr = fmt.Errorf("configuracion de identidad incompleta")
			return
		}
		instance = &App{DDB: awsddb.New(a), SSM: ssm.NewFromConfig(a), Table: table, SecretParam: param, ResetEmailParam: resetEmailParam}
	})
	return instance, appErr
}

func (a *App) jwtSecret(ctx context.Context) (string, error) {
	a.secretOnce.Do(func() {
		output, err := a.SSM.GetParameter(ctx, &ssm.GetParameterInput{Name: &a.SecretParam, WithDecryption: aws.Bool(true)})
		if err != nil {
			a.secretErr = fmt.Errorf("leer secreto JWT: %w", err)
			return
		}
		if output.Parameter == nil || output.Parameter.Value == nil {
			a.secretErr = fmt.Errorf("leer secreto JWT: parámetro vacío")
			return
		}
		a.secret = *output.Parameter.Value
	})
	return a.secret, a.secretErr
}
