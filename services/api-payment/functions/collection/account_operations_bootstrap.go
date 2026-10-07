package collection

import (
	"context"
	"os"
	"time"
	_ "time/tzdata" // Calendario chileno disponible también en Lambda.

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
)

// AccountOperationsHandler valida identidad administrativa antes de inicializar AWS.
func AccountOperationsHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, err := lambdautil.UserIDFromContext(req); err != nil {
		return adminFailure(req, 401, "UNAUTHORIZED")
	}
	if req.RequestContext.Authorizer.Lambda["paymentAccess"] != "admin" {
		return adminFailure(req, 403, "FORBIDDEN")
	}
	if os.Getenv("APP_STAGE") != "dev" || os.Getenv("PAYMENTS_TABLE_NAME") == "" {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return domainFailure(req, err)
	}
	a := AccountOperationsApp{Accounts: Service{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")}, Now: time.Now}
	return a.Handle(ctx, req)
}
