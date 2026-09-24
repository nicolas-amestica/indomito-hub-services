package rates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

const (
	bcchBaseURL   = "https://si3.bcentral.cl/SieteRestWS/SieteRestWS.ashx"
	bcchUSDSeries = "F073.TCO.PRE.Z.D"
	bcchBRLSeries = "F072.CLP.BRL.N.O.D"
	providerBCCH  = "banco-central"
)

const (
	providerResolvedEvent = "ratesProviderResolved"
	providerFailedEvent   = "ratesProviderFailed"
)

// PreferredSources contiene las dependencias de las fuentes priorizadas.
// Los endpoints son campos para que las pruebas no dependan de Internet.
type PreferredSources struct {
	HTTPClient       HTTPDoer
	BCCHToken        string
	BCCHURL          string
	CurrencyFallback Source
	Now              func() time.Time
}

// DefaultPreferredSources construye la cadena usada en AWS.
func DefaultPreferredSources(bcchToken string) PreferredSources {
	client := &http.Client{}
	return PreferredSources{
		HTTPClient: client,
		BCCHToken:  bcchToken,
		BCCHURL:    bcchBaseURL,
		CurrencyFallback: Source{
			HTTPClient: client,
			BaseURL:    ratesBaseURL,
		},
		Now: time.Now,
	}
}

func (s PreferredSources) withDefaults() PreferredSources {
	defaults := DefaultPreferredSources(s.BCCHToken)
	if s.HTTPClient == nil {
		s.HTTPClient = defaults.HTTPClient
	}
	if s.BCCHURL == "" {
		s.BCCHURL = defaults.BCCHURL
	}
	if s.CurrencyFallback.HTTPClient == nil {
		s.CurrencyFallback.HTTPClient = s.HTTPClient
	}
	if s.CurrencyFallback.BaseURL == "" {
		s.CurrencyFallback.BaseURL = ratesBaseURL
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	return s
}

// FetchPreferredRates consulta Banco Central primero y usa
// @fawazahmed0/currency-api cuando aquel no está configurado o no responde.
// DynamoDB permanece fuera de esta función y sigue siendo el último respaldo
// del endpoint.
func FetchPreferredRates(
	ctx context.Context,
	sources PreferredSources,
	log *zap.Logger,
) (FetchedRates, error) {
	sources = sources.withDefaults()
	var providerErrors []error

	if strings.TrimSpace(sources.BCCHToken) != "" {
		rates, err := fetchProviderWithRetries(ctx, providerBCCH, log, func(ctx context.Context) (FetchedRates, error) {
			return sources.fetchBCCH(ctx, log)
		})
		if err == nil {
			return rates, nil
		}
		providerErrors = append(providerErrors, err)
	} else {
		log.Warn(providerFailedEvent,
			zap.String("provider", providerBCCH),
			zap.String("reason", "tokenNotConfigured"),
		)
	}

	rates, err := FetchRates(ctx, sources.CurrencyFallback, log)
	if err == nil {
		log.Info(providerResolvedEvent,
			zap.String("provider", string(program.ExchangeRateSourceCurrencyAPI)),
			zap.Int("attempt", rates.Attempts),
		)
		return rates, nil
	}
	providerErrors = append(providerErrors, err)

	return FetchedRates{}, apperr.
		UpstreamServiceError("No se pudieron obtener los tipos de cambio").
		WithCause(errors.Join(providerErrors...))
}

func fetchProviderWithRetries(
	ctx context.Context,
	provider string,
	log *zap.Logger,
	fetch func(context.Context) (FetchedRates, error),
) (FetchedRates, error) {
	var lastErr error
	for attempt := 1; attempt <= ratesMaxAttempts; attempt++ {
		startedAt := time.Now()
		rates, err := fetch(ctx)
		if err == nil {
			rates.Attempts = attempt
			rates.UpstreamLatency = time.Since(startedAt)
			log.Info(providerResolvedEvent,
				zap.String("provider", provider),
				zap.Int("attempt", attempt),
			)
			return rates, nil
		}

		lastErr = err
		log.Warn(providerFailedEvent,
			zap.String("provider", provider),
			zap.Int("attempt", attempt),
			zap.Int("maxAttempts", ratesMaxAttempts),
			zap.Error(err),
		)
		if attempt < ratesMaxAttempts {
			if waitErr := waitBeforeRetry(ctx, backoffFor(attempt)); waitErr != nil {
				return FetchedRates{}, errors.Join(lastErr, waitErr)
			}
		}
	}
	return FetchedRates{}, fmt.Errorf("%s agotó sus intentos: %w", provider, lastErr)
}

func (s PreferredSources) fetchBCCH(ctx context.Context, log *zap.Logger) (FetchedRates, error) {
	from := s.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02")
	to := s.Now().UTC().Format("2006-01-02")

	usd, brl, err := fetchPair(ctx,
		func(ctx context.Context) (currencyQuote, error) {
			return s.fetchBCCHSeries(ctx, bcchUSDSeries, from, to)
		},
		func(ctx context.Context) (currencyQuote, error) {
			return s.fetchBCCHSeries(ctx, bcchBRLSeries, from, to)
		},
	)
	if err != nil {
		return FetchedRates{}, err
	}

	return fetchedRatesFromQuotes(usd, brl, program.ExchangeRateSourceBCCH, log), nil
}

func (s PreferredSources) fetchBCCHSeries(
	ctx context.Context,
	series string,
	firstDate string,
	lastDate string,
) (currencyQuote, error) {
	queryCtx, cancel := context.WithTimeout(ctx, ratesTimeout)
	defer cancel()

	endpoint, err := url.Parse(s.BCCHURL)
	if err != nil {
		return currencyQuote{}, fmt.Errorf("URL de Banco Central inválida: %w", err)
	}
	query := endpoint.Query()
	query.Set("token", s.BCCHToken)
	query.Set("function", "GetSeries")
	query.Set("timeseries", series)
	query.Set("firstdate", firstDate)
	query.Set("lastdate", lastDate)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(queryCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return currencyQuote{}, fmt.Errorf("no se pudo armar consulta al Banco Central: %w", err)
	}
	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return currencyQuote{}, fmt.Errorf("consulta al Banco Central falló: %w", err)
	}
	defer drainAndClose(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return currencyQuote{}, fmt.Errorf("Banco Central respondió %d", resp.StatusCode)
	}

	return decodeBCCHQuote(resp.Body, series)
}

func decodeBCCHQuote(body io.Reader, series string) (currencyQuote, error) {
	var response struct {
		Code        int    `json:"Codigo"`
		Description string `json:"Descripcion"`
		Series      struct {
			Observations []struct {
				Date   string `json:"indexDateString"`
				Value  string `json:"value"`
				Status string `json:"statusCode"`
			} `json:"Obs"`
		} `json:"Series"`
	}
	if err := json.NewDecoder(io.LimitReader(body, maxBodyBytes)).Decode(&response); err != nil {
		return currencyQuote{}, fmt.Errorf("respuesta de Banco Central no interpretable")
	}
	if response.Code != 0 {
		return currencyQuote{}, fmt.Errorf("Banco Central rechazó la serie %s: %s", series, response.Description)
	}

	for index := len(response.Series.Observations) - 1; index >= 0; index-- {
		observation := response.Series.Observations[index]
		if observation.Status != "OK" || observation.Value == "" {
			continue
		}
		value, err := strconv.ParseFloat(strings.ReplaceAll(observation.Value, ",", "."), 64)
		if err != nil || value <= 0 {
			continue
		}
		date, err := time.Parse("02-01-2006", observation.Date)
		if err != nil {
			continue
		}
		return currencyQuote{date: date.Format("2006-01-02"), toClp: value}, nil
	}

	return currencyQuote{}, fmt.Errorf("Banco Central no entregó una observación válida para %s", series)
}

func fetchPair(
	ctx context.Context,
	first func(context.Context) (currencyQuote, error),
	second func(context.Context) (currencyQuote, error),
) (currencyQuote, currencyQuote, error) {
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var firstQuote, secondQuote currencyQuote
	var firstErr, secondErr error
	var wait sync.WaitGroup
	wait.Add(2)

	go func() {
		defer wait.Done()
		firstQuote, firstErr = first(attemptCtx)
		if firstErr != nil {
			cancel()
		}
	}()
	go func() {
		defer wait.Done()
		secondQuote, secondErr = second(attemptCtx)
		if secondErr != nil {
			cancel()
		}
	}()
	wait.Wait()

	if firstErr != nil || secondErr != nil {
		return currencyQuote{}, currencyQuote{}, errors.Join(firstErr, secondErr)
	}
	return firstQuote, secondQuote, nil
}

func fetchedRatesFromQuotes(
	usd currencyQuote,
	brl currencyQuote,
	source program.ExchangeRateSource,
	log *zap.Logger,
) FetchedRates {
	return FetchedRates{
		Date:     resolveDate(usd, brl, log),
		UsdToClp: roundToClp(usd.toClp),
		BrlToClp: roundToClp(brl.toClp),
		Source:   source,
	}
}

func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxBodyBytes))
	_ = body.Close()
}
