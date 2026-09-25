// Package crearfavoritov1 implementa POST /favoritos para el usuario
// autenticado. La identidad siempre sale del contexto del authorizer y nunca
// del cuerpo de la solicitud (Requirement 19.8).
package crearfavoritov1

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	"ind-hub-api-gox-sls-pri-gh/services/api-favorite/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-favorite/functions"
)

const operationName = "create"

// Request es el cuerpo de POST /favoritos. Scope no forma parte de la
// solicitud: este endpoint crea exclusivamente favoritos de programa.
type Request struct {
	Name    string                 `json:"name"    validate:"required"`
	Content domain.FavoriteContent `json:"content" validate:"required"`
}

var _ functions.RegisterFunc = Register

// Handle procesa la invocación de fn-crear-favorito-v1 en AWS.
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

// Register expone el mismo handler en el servidor local de desarrollo.
func Register(e *echo.Echo, app *functions.App, _ *zap.Logger) {
	functions.CreateFavoriteRoute.Register(e, lambdautil.EchoAdapter(
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
		logger.WarnOrError(log, err, "createFavoriteRejected",
			zap.String("operation", operationName),
		)

		return lambdautil.ErrorResponse(req, err)
	}

	log = log.With(
		zap.String("userId", userID),
		zap.String("operation", operationName),
	)

	var request Request
	if err := lambdautil.BindJSON(req, &request); err != nil {
		logger.WarnOrError(log, err, "createFavoriteRejected")

		return lambdautil.ErrorResponse(req, err)
	}

	request.Name = strings.TrimSpace(request.Name)
	if err := validateRequest(request); err != nil {
		logger.WarnOrError(log, err, "createFavoriteRejected")

		return lambdautil.ErrorResponse(req, err)
	}

	favoriteID := domain.NewFavoriteID()
	now := time.Now().UTC()
	favorite, err := createFavorite(
		ctx,
		app.DDB,
		app.Config.FavoritesTableName,
		userID,
		favoriteID,
		request,
		now,
	)
	if err != nil {
		logger.WarnOrError(log, err, "createFavoriteFailed",
			zap.String("favoriteId", favoriteID),
		)

		return lambdautil.ErrorResponse(req, err)
	}

	log.Info("favoriteCreated", zap.String("favoriteId", favorite.ID))

	return lambdautil.SuccessResponse(http.StatusCreated, favorite)
}

// validateRequest aplica las etiquetas de los tipos compartidos de programa.
// Recortar Name antes de validar impide que una cadena de espacios pase la
// regla required y termine convertida en un error interno del dominio.
func validateRequest(request Request) error {
	if strings.TrimSpace(request.Name) == "" {
		return apperr.
			RequiredFieldMissing("El campo name es obligatorio").
			WithDetails(map[string]any{"field": "name", "rule": "required"})
	}

	return lambdautil.ValidateStruct(request)
}

// createFavorite construye y persiste un favorito con dependencias y valores
// deterministas, para que el handler pueda probarse sin reloj ni AWS.
func createFavorite(
	ctx context.Context,
	ddb awsddb.Client,
	tableName string,
	userID string,
	favoriteID string,
	request Request,
	now time.Time,
) (domain.Favorite, error) {
	item, err := domain.NewFavoriteItem(
		domain.FavoriteKey{
			UserID: userID,
			Scope:  domain.ScopeQuotation,
			ID:     favoriteID,
		},
		request.Name,
		request.Content,
		now,
		now,
	)
	if err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo construir el favorito: %w", err))
	}

	rawItem, err := attributevalue.MarshalMap(item)
	if err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo serializar el favorito: %w", err))
	}

	if _, err := ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(tableName),
		Item:                rawItem,
		ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)"),
	}); err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo guardar el favorito: %w", err))
	}

	favorite, err := item.Favorite()
	if err != nil {
		return domain.Favorite{}, apperr.Internal(fmt.Errorf("no se pudo construir la respuesta del favorito: %w", err))
	}

	return favorite, nil
}
