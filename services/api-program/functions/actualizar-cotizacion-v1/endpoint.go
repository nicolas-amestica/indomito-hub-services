// Package actualizarfavoritov1 implementa PUT /favoritos/{id-favorito} para
// reemplazar el nombre y el contenido de un favorito del usuario autenticado.
package actualizarcotizacionv1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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

const operationName = "update"

// Request reemplaza por completo los datos editables del favorito. Las fechas
// de creación y la identidad se preservan en DynamoDB.
type Request struct {
	Name    string                 `json:"name"    validate:"required"`
	Content domain.FavoriteContent `json:"content" validate:"required"`
}

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
	functions.UpdateQuotationRoute.Register(e, lambdautil.EchoAdapter(
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
		logger.WarnOrError(log, err, "updateFavoriteRejected",
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
		logger.WarnOrError(log, err, "updateFavoriteRejected")

		return lambdautil.ErrorResponse(req, err)
	}

	var request Request
	if err := lambdautil.BindJSON(req, &request); err != nil {
		logger.WarnOrError(log, err, "updateFavoriteRejected")

		return lambdautil.ErrorResponse(req, err)
	}

	request.Name = strings.TrimSpace(request.Name)
	if err := validateRequest(request); err != nil {
		logger.WarnOrError(log, err, "updateFavoriteRejected")

		return lambdautil.ErrorResponse(req, err)
	}

	favorite, err := updateFavorite(
		ctx,
		app.DDB,
		app.Config.ProgramsTableName,
		userID,
		favoriteID,
		request,
		time.Now().UTC(),
	)
	if err != nil {
		logger.WarnOrError(log, err, "updateFavoriteFailed")

		return lambdautil.ErrorResponse(req, err)
	}

	log.Info("favoriteUpdated")

	return lambdautil.SuccessResponse(http.StatusOK, favorite)
}

func validateFavoriteID(favoriteID string) error {
	if err := domain.ValidateFavoriteID(favoriteID); err != nil {
		return apperr.
			Validation("El identificador del favorito no es válido").
			WithDetails(map[string]any{"field": functions.QuotationIDParam})
	}

	return nil
}

func validateRequest(request Request) error {
	if strings.TrimSpace(request.Name) == "" {
		return apperr.
			RequiredFieldMissing("El campo name es obligatorio").
			WithDetails(map[string]any{"field": "name", "rule": "required"})
	}

	return lambdautil.ValidateStruct(request)
}

func updateFavorite(
	ctx context.Context,
	ddb awsddb.Client,
	tableName string,
	userID string,
	favoriteID string,
	request Request,
	updatedAt time.Time,
) (domain.Favorite, error) {
	key, err := (domain.FavoriteKey{
		UserID: userID,
		Scope:  domain.ScopeQuotation,
		ID:     favoriteID,
	}).Key()
	if err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo construir la clave del favorito: %w", err))
	}

	rawKey, err := attributevalue.MarshalMap(key)
	if err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo serializar la clave del favorito: %w", err))
	}

	values, err := attributevalue.MarshalMap(map[string]any{
		":name":      request.Name,
		":content":   request.Content,
		":updatedAt": updatedAt.UTC(),
	})
	if err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo serializar la actualización del favorito: %w", err))
	}

	output, err := ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(tableName),
		Key:       rawKey,
		ExpressionAttributeNames: map[string]string{
			"#name":      "name",
			"#content":   "content",
			"#updatedAt": "updatedAt",
		},
		ExpressionAttributeValues: values,
		UpdateExpression:          aws.String("SET #name = :name, #content = :content, #updatedAt = :updatedAt"),
		ConditionExpression:       aws.String("attribute_exists(pk) AND attribute_exists(sk)"),
		ReturnValues:              types.ReturnValueAllNew,
	})
	if err != nil {
		var conditionalFailure *types.ConditionalCheckFailedException
		if errors.As(err, &conditionalFailure) {
			return domain.Favorite{}, apperr.NotFound("El favorito no existe")
		}

		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo actualizar el favorito: %w", err))
	}
	if output == nil || len(output.Attributes) == 0 {
		return domain.Favorite{}, apperr.Internal(errors.New("DynamoDB no devolvió el favorito actualizado"))
	}

	var item domain.FavoriteItem
	if err := attributevalue.UnmarshalMap(output.Attributes, &item); err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo leer el favorito actualizado: %w", err))
	}

	favorite, err := item.Favorite()
	if err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo construir la respuesta del favorito: %w", err))
	}

	return favorite, nil
}
