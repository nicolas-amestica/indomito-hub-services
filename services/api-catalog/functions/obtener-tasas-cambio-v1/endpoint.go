// Package rates implementa GET /tasas-cambio: consulta USD y BRL en la fuente
// externa y usa el último snapshot persistido solo como respaldo de
// disponibilidad (Requirement 14).
package rates

import (
	"context"
	"net/http"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/logger"

	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions"
)

const (
	ratesResolvedEvent      = "ratesResolved"
	ratesUnavailableEvent   = "ratesUnavailable"
	fallbackDeliveredEvent  = "ratesFallbackDelivered"
	snapshotReadErrorEvent  = "ratesSnapshotReadError"
	snapshotWriteErrorEvent = "ratesSnapshotWriteError"
)

type fetchRatesFunc func(context.Context, Source, *zap.Logger) (FetchedRates, error)

// Register registra GET /tasas-cambio en el servidor local de Echo.
func Register(e *echo.Echo, app *functions.App, _ *zap.Logger) {
	functions.ExchangeRatesRoute.Register(e, lambdautil.EchoAdapter(
		func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
			return handle(ctx, app, req)
		},
	))
}

// Handle procesa una invocación Lambda de GET /tasas-cambio.
func Handle(
	ctx context.Context,
	req events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}

	return handle(ctx, app, req)
}

func handle(
	ctx context.Context,
	app *functions.App,
	req events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error) {
	log := app.Logger(req.RequestContext.RequestID)
	defer func() {
		_ = log.Sync()
	}()

	preferredSources := DefaultPreferredSources(app.Config.BCCHAPIToken)
	return serveRates(ctx, app, req, DefaultSource(), func(
		ctx context.Context,
		_ Source,
		log *zap.Logger,
	) (FetchedRates, error) {
		return FetchPreferredRates(ctx, preferredSources, log)
	}, time.Now, log)
}

// serveRates contiene el flujo comprobable del endpoint con sus dependencias
// explícitas. La fuente se consulta siempre antes de intentar leer el snapshot:
// este último es un respaldo, no un caché.
func serveRates(
	ctx context.Context,
	app *functions.App,
	req events.APIGatewayV2HTTPRequest,
	source Source,
	fetch fetchRatesFunc,
	now func() time.Time,
	log *zap.Logger,
) (events.APIGatewayV2HTTPResponse, error) {
	fetched, fetchErr := fetch(ctx, source, log)
	if fetchErr == nil {
		snapshot := fetched.Snapshot()

		// La respuesta se arma antes del PutItem. Si la escritura falla, las
		// tasas frescas siguen siendo correctas y se responden de todas formas.
		response, responseErr := lambdautil.SuccessResponse(http.StatusOK, snapshot)
		if responseErr != nil {
			return response, responseErr
		}

		if err := WriteRateSnapshot(ctx, app.DDB, app.Config.CatalogsTableName, snapshot, now()); err != nil {
			log.Warn(snapshotWriteErrorEvent,
				zap.String("snapshotWriteError", err.Error()),
			)
		}

		log.Info(ratesResolvedEvent,
			zap.Int("attempt", fetched.Attempts),
			zap.Int64("upstreamLatencyMs", fetched.UpstreamLatencyMs()),
			zap.Int64("usdToClp", fetched.UsdToClp),
			zap.Int64("brlToClp", fetched.BrlToClp),
			zap.String("rateDate", fetched.Date),
		)

		return response, nil
	}

	item, found, err := ReadRateSnapshot(ctx, app.DDB, app.Config.CatalogsTableName)
	if err != nil {
		logger.WarnOrError(log, err, snapshotReadErrorEvent)
		return lambdautil.ErrorResponse(req, err)
	}

	if !found {
		logger.WarnOrError(log, fetchErr, ratesUnavailableEvent,
			zap.Int("attempts", fetched.Attempts),
		)
		return lambdautil.ErrorResponse(req, fetchErr)
	}

	log.Warn(fallbackDeliveredEvent,
		zap.String("snapshotDate", item.Date),
		zap.Time("snapshotFetchedAt", item.FetchedAt),
		zap.Int("attempts", fetched.Attempts),
	)

	return lambdautil.SuccessResponse(http.StatusOK, item.FallbackSnapshot())
}
