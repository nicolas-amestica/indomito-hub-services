// Package generarpresupuestov1 implementa POST /programas:presupuesto. Recibe
// precios ya calculados, valida que su forma sea posible y devuelve un PDF en
// base64 sin persistirlo.
package generarpresupuestov1

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/logger"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions/generar-presupuesto-v1/templates"
)

const (
	contentTypePDF = "application/pdf"
	operationName  = "generateBudget"
)

var _ functions.RegisterFunc = Register

// Handle procesa la invocación de fn-generar-presupuesto-v1 en AWS.
func Handle(
	ctx context.Context,
	request events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(request, err)
	}

	return handle(
		ctx,
		app,
		app.Logger(request.RequestContext.RequestID),
		request,
		templates.DefaultRegistry(),
		time.Now().UTC(),
	)
}

// Register expone el mismo handler en el servidor local de desarrollo.
func Register(e *echo.Echo, app *functions.App, _ *zap.Logger) {
	functions.GenerateBudgetRoute.Register(e, lambdautil.EchoAdapter(
		func(
			ctx context.Context,
			request events.APIGatewayV2HTTPRequest,
		) (events.APIGatewayV2HTTPResponse, error) {
			return handle(
				ctx,
				app,
				app.Logger(request.RequestContext.RequestID),
				request,
				templates.DefaultRegistry(),
				time.Now().UTC(),
			)
		},
	))
}

func handle(
	_ context.Context,
	_ *functions.App,
	log *zap.Logger,
	httpRequest events.APIGatewayV2HTTPRequest,
	registry templates.Registry,
	generatedAt time.Time,
) (events.APIGatewayV2HTTPResponse, error) {
	if _, err := lambdautil.UserIDFromContext(httpRequest); err != nil {
		logger.WarnOrError(log, err, "generateBudgetRejected",
			zap.String("operation", operationName),
			zap.String("reason", "missingAuthorizerIdentity"),
			zap.Int("scenarioIndex", globalValidationScenarioIndex),
		)
		return lambdautil.ErrorResponse(httpRequest, err)
	}

	var request domain.BudgetRequest
	if err := lambdautil.BindJSON(httpRequest, &request); err != nil {
		logRejection(log, err)
		return lambdautil.ErrorResponse(httpRequest, err)
	}
	if err := ValidateRequest(request, registry); err != nil {
		logRejection(log, err)
		return lambdautil.ErrorResponse(httpRequest, err)
	}

	template, ok := registry.Lookup(request.Destination.BudgetTemplateID)
	if !ok {
		// ValidateRequest ya cubre este caso. La rama conserva el contrato si el
		// registro alguna vez pasa a ser dinámico entre ambas operaciones.
		err := apperr.Internal(errors.New("la plantilla validada desapareció del registro"))
		logger.WarnOrError(log, err, "generateBudgetFailed")
		return lambdautil.ErrorResponse(httpRequest, err)
	}

	pdfBytes, err := ComposePDF(request, template, generatedAt)
	if err != nil {
		err = apperr.Internal(err)
		logger.WarnOrError(log, err, "generateBudgetFailed",
			zap.String("budgetTemplateId", template.ID()),
			zap.Int("scenarioCount", len(request.Scenarios)),
		)
		return lambdautil.ErrorResponse(httpRequest, err)
	}

	log.Info("budgetGenerated",
		zap.String("budgetTemplateId", template.ID()),
		zap.Int("scenarioCount", len(request.Scenarios)),
		zap.Int("pdfBytes", len(pdfBytes)),
	)

	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusOK,
		Headers: map[string]string{
			"Content-Type":        contentTypePDF,
			"Content-Disposition": `inline; filename="presupuesto.pdf"`,
			"Cache-Control":       "no-store",
		},
		Body:            base64.StdEncoding.EncodeToString(pdfBytes),
		IsBase64Encoded: true,
	}, nil
}

func logRejection(log *zap.Logger, err error) {
	reason := reasonShapeValidation
	scenarioIndex := globalValidationScenarioIndex

	var appErr *apperr.AppError
	if errors.As(err, &appErr) {
		if value, ok := appErr.Details()["reason"].(string); ok {
			reason = value
		}
		if value, ok := appErr.Details()["scenarioIndex"].(int); ok {
			scenarioIndex = value
		}
	}

	logger.WarnOrError(log, err, "generateBudgetRejected",
		zap.String("operation", operationName),
		zap.String("reason", reason),
		zap.Int("scenarioIndex", scenarioIndex),
	)
}
