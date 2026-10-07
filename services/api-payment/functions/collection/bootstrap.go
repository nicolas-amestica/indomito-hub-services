package collection

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

var adminMu sync.Mutex
var adminInstance *AdminApp
var adminLookupSecret string

func getAdminApp(ctx context.Context) (*AdminApp, error) {
	adminMu.Lock()
	defer adminMu.Unlock()
	if adminInstance != nil {
		return adminInstance, nil
	}
	if os.Getenv("APP_STAGE") != "dev" || os.Getenv("PAYMENTS_TABLE_NAME") == "" || os.Getenv("PROGRAMS_TABLE_NAME") == "" {
		return nil, errors.New("configuracion administrativa DEV incompleta")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return nil, err
	}
	db := dynamodb.NewFromConfig(cfg)
	adminInstance = &AdminApp{Accounts: Service{DB: db, Table: os.Getenv("PAYMENTS_TABLE_NAME")}, Contracts: ContractSource{DB: db, Table: os.Getenv("PROGRAMS_TABLE_NAME")}}
	return adminInstance, nil
}

// getAdminAppWithLookup carga el secreto solo para operaciones que calculan
// relaciones HMAC (puesta en marcha, altas por anexo o rotación de código).
// Las consultas administrativas comunes no deben depender de SSM.
func getAdminAppWithLookup(ctx context.Context) (*AdminApp, error) {
	base, err := getAdminApp(ctx)
	if err != nil {
		return nil, err
	}
	adminMu.Lock()
	defer adminMu.Unlock()
	if adminLookupSecret == "" {
		cfg, configErr := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
		if configErr != nil {
			return nil, configErr
		}
		parameter, err := ssm.NewFromConfig(cfg).GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String("/indomito/dev/payments/lookup-secret"), WithDecryption: aws.Bool(true)})
		if err != nil || parameter == nil || parameter.Parameter == nil || len(aws.ToString(parameter.Parameter.Value)) < 32 {
			return nil, errors.New("configuración de búsqueda incompleta")
		}
		adminLookupSecret = aws.ToString(parameter.Parameter.Value)
	}
	secured := *base
	secured.LookupSecret = adminLookupSecret
	return &secured, nil
}

// LookupHandler no requiere sesión previa: RUT+código autorizan solo esta consulta mínima.
// Las futuras acciones de checkout/comprobantes requieren una sesión acotada validada por el authorizer.
func LookupHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getPublicApp(ctx)
	if err != nil {
		return lookupFailure(req, apperr.CodeUpstreamServiceError)
	}
	return app.HandleLookup(ctx, req)
}

// SetupHandler es la entrada Lambda privada; nunca acepta el simulador ni tokens en el cuerpo.
func SetupHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, err := lambdautil.UserIDFromContext(req); err != nil {
		return adminFailure(req, 401, "UNAUTHORIZED")
	}
	if req.RequestContext.Authorizer.Lambda["paymentAccess"] != "admin" {
		return adminFailure(req, 403, "FORBIDDEN")
	}
	app, err := getAdminAppWithLookup(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return app.HandleSetup(ctx, req)
}
