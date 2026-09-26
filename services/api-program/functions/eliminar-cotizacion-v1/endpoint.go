// Package eliminarfavoritov1 implementa DELETE /favoritos/{id-favorito} para
// eliminar un favorito del usuario autenticado.
package eliminarcotizacionv1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/logger"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions"
)

const operationName = "delete"

var _ functions.RegisterFunc = Register

func Handle(
	ctx context.Context,
	req events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}

	return handle(ctx, app, app.Logger(req.RequestContext.RequestID), req)
}

func Register(e *echo.Echo, app *functions.App, _ *zap.Logger) {
	functions.DeleteQuotationRoute.Register(e, lambdautil.EchoAdapter(
		func(
			ctx context.Context,
			req events.APIGatewayV2HTTPRequest,
		) (events.APIGatewayV2HTTPResponse, error) {
			return handle(ctx, app, app.Logger(req.RequestContext.RequestID), req)
		},
	))
}

func handle(
	ctx context.Context,
	app *functions.App,
	log *zap.Logger,
	req events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error) {
	userID, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		logger.WarnOrError(log, err, "deleteFavoriteRejected",
			zap.String("operation", operationName),
		)

		return lambdautil.ErrorResponse(req, err)
	}

	favoriteID := strings.TrimSpace(req.PathParameters[functions.QuotationIDParam])
	log = log.With(
		zap.String("favoriteId", favoriteID),
		zap.String("userId", userID),
		zap.String("operation", operationName),
	)

	if err := validateFavoriteID(favoriteID); err != nil {
		logger.WarnOrError(log, err, "deleteFavoriteRejected")

		return lambdautil.ErrorResponse(req, err)
	}

	if err := deleteFavorite(
		ctx,
		app.DDB,
		app.Config.ProgramsTableName,
		userID,
		favoriteID,
	); err != nil {
		logger.WarnOrError(log, err, "deleteFavoriteFailed")

		return lambdautil.ErrorResponse(req, err)
	}

	log.Info("favoriteDeleted")

	return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusNoContent}, nil
}

func validateFavoriteID(favoriteID string) error {
	if err := domain.ValidateFavoriteID(favoriteID); err != nil {
		return apperr.
			Validation("El identificador del favorito no es válido").
			WithDetails(map[string]any{"field": functions.QuotationIDParam})
	}

	return nil
}

func deleteFavorite(
	ctx context.Context,
	ddb awsddb.Client,
	tableName string,
	userID string,
	favoriteID string,
) error {
	key, err := (domain.FavoriteKey{
		UserID: userID,
		Scope:  domain.ScopeQuotation,
		ID:     favoriteID,
	}).Key()
	if err != nil {
		return apperr.Internal(fmt.Errorf("no se pudo construir la clave del favorito: %w", err))
	}

	rawKey, err := attributevalue.MarshalMap(key)
	if err != nil {
		return apperr.Internal(fmt.Errorf("no se pudo serializar la clave del favorito: %w", err))
	}

	_, err = ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:           aws.String(tableName),
		Key:                 rawKey,
		ConditionExpression: aws.String("attribute_exists(pk) AND attribute_exists(sk)"),
	})
	if err != nil {
		var conditionalFailure *types.ConditionalCheckFailedException
		if errors.As(err, &conditionalFailure) {
			return apperr.NotFound("El favorito no existe")
		}

		return apperr.Internal(fmt.Errorf("no se pudo eliminar el favorito: %w", err))
	}

	return nil
}
