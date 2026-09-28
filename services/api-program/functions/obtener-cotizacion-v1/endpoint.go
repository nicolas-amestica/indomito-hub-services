// Package obtenercotizacionv1 implementa GET /cotizaciones/{id-cotizacion}.
package obtenercotizacionv1

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/logger"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions"
)

const operationName = "get"

var _ functions.RegisterFunc = Register

func Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}

	return handle(ctx, app, app.Logger(req.RequestContext.RequestID), req)
}

func Register(e *echo.Echo, app *functions.App, _ *zap.Logger) {
	functions.GetQuotationRoute.Register(e, lambdautil.EchoAdapter(
		func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
			return handle(ctx, app, app.Logger(req.RequestContext.RequestID), req)
		},
	))
}

func handle(ctx context.Context, app *functions.App, log *zap.Logger, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	userID, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		logger.WarnOrError(log, err, "getQuotationRejected", zap.String("operation", operationName))
		return lambdautil.ErrorResponse(req, err)
	}

	quotationID := strings.TrimSpace(req.PathParameters[functions.QuotationIDParam])
	log = log.With(zap.String("userId", userID), zap.String("quotationId", quotationID), zap.String("operation", operationName))
	if err := domain.ValidateFavoriteID(quotationID); err != nil {
		validationErr := apperr.Validation("El identificador de la cotización no es válido").
			WithDetails(map[string]any{"field": functions.QuotationIDParam})
		logger.WarnOrError(log, validationErr, "getQuotationRejected")
		return lambdautil.ErrorResponse(req, validationErr)
	}

	quotation, err := getQuotation(ctx, app.DDB, app.Config.ProgramsTableName, userID, quotationID)
	if err != nil {
		logger.WarnOrError(log, err, "getQuotationFailed")
		return lambdautil.ErrorResponse(req, err)
	}

	log.Info("quotationObtained")
	return lambdautil.SuccessResponse(http.StatusOK, quotation)
}

func getQuotation(ctx context.Context, ddb awsddb.Client, tableName, userID, quotationID string) (domain.Favorite, error) {
	key, err := (domain.FavoriteKey{UserID: userID, Scope: domain.ScopeQuotation, ID: quotationID}).Key()
	if err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo construir la clave de la cotización: %w", err))
	}
	rawKey, err := attributevalue.MarshalMap(key)
	if err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo serializar la clave de la cotización: %w", err))
	}

	output, err := ddb.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(tableName), Key: rawKey, ConsistentRead: aws.Bool(false)})
	if err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo obtener la cotización: %w", err))
	}
	if output == nil || len(output.Item) == 0 {
		return domain.Favorite{}, apperr.NotFound("La cotización no existe")
	}

	var item domain.FavoriteItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo leer la cotización: %w", err))
	}
	favorite, err := item.Favorite()
	if err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo construir la cotización: %w", err))
	}

	return favorite, nil
}
