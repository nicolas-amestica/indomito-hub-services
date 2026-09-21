package rates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

// Parámetros de la consulta a la fuente externa. Replican los del handler
// legacy, que está verificado en producción: misma fuente, mismo límite por
// consulta y misma política de espera creciente.
const (
	// ratesBaseURL es la raíz de `@fawazahmed0/currency-api` servida por la CDN
	// de jsDelivr. El sufijo `@latest` lo resuelve jsDelivr al último publicado,
	// así que la URL no hay que versionarla acá.
	ratesBaseURL = "https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies"

	// ratesTimeout es el límite de cada consulta, no el del conjunto
	// (Requirement 14.11). Se aplica por petición y por intento: dos divisas en
	// paralelo con tres intentos son seis peticiones, cada una con sus 10 s.
	ratesTimeout = 10 * time.Second

	// ratesMaxAttempts es la cantidad total de intentos, incluido el primero
	// (Requirement 14.6).
	//
	// Total y no "reintentos después del primero": el diagrama de flujo del
	// diseño describe el caso de falla como "los 3 intentos fallan", y el campo
	// `attempts` del log de respaldo cuenta intentos, no reintentos. Nombrarlo
	// `ratesMaxRetry` invitaría a leer cuatro peticiones por divisa donde el
	// diseño dimensionó tres.
	ratesMaxAttempts = 3

	// ratesBackoff es la espera antes del segundo intento. Cada intento
	// siguiente la multiplica por ratesBackoffFactor: 500 ms y 1 s.
	ratesBackoff = 500 * time.Millisecond

	// ratesBackoffFactor es el factor de crecimiento de la espera.
	ratesBackoffFactor = 2
)

// Divisas de la consulta, en minúscula porque así se nombran tanto el documento
// de la fuente (`usd.json`) como las claves de su cuerpo.
const (
	currencyUSD = "usd"
	currencyBRL = "brl"
	currencyCLP = "clp"
)

// dateField es la clave de la fecha en el documento de la fuente. Conviene
// tenerla aparte porque comparte el nivel del cuerpo con la clave de la divisa,
// y es lo que obliga a interpretar el documento clave por clave.
const dateField = "date"

// maxBodyBytes limita lo que se lee del cuerpo de la fuente externa.
//
// El documento de una divisa trae unas doscientas cotizaciones y no llega a
// 20 KB; un megabyte deja margen de sobra para que crezca. Existe porque el
// cuerpo lo produce un tercero: sin límite, una respuesta anómala se leería
// entera en la memoria de una Lambda de 128 MB.
const maxBodyBytes = 1 << 20

// Eventos de log de la consulta.
const (
	// attemptFailedEvent registra un intento fallido. No es un error del
	// endpoint todavía: quedan reintentos, y si alguno funciona la petición
	// termina en 200.
	attemptFailedEvent = "ratesAttemptFailed"

	// divergentDatesEvent registra que las dos divisas informaron fechas
	// distintas (Requirement 14.12).
	divergentDatesEvent = "ratesDivergentDates"
)

// HTTPDoer es la superficie de [http.Client] que necesita la consulta.
//
// Se declara como interfaz por el mismo motivo que awsddb.Client en el otro
// endpoint del servicio: permite ejercitar los reintentos, el redondeo y la
// divergencia de fechas con un doble, sin depender de que la fuente externa
// esté disponible ni de cuánto tarde.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Source es la fuente externa de tasas: el cliente HTTP con el que se la
// consulta y la raíz de sus documentos.
//
// La raíz es un campo y no la constante directa porque los tests apuntan a un
// servidor local. En producción se construye con [DefaultSource] y nadie la
// elige: es [ratesBaseURL].
type Source struct {
	// HTTPClient hace las peticiones. Si es nil se usa un cliente propio.
	HTTPClient HTTPDoer

	// BaseURL es la raíz de los documentos por divisa, sin barra final. Si está
	// vacía se usa [ratesBaseURL].
	BaseURL string
}

// DefaultSource es la fuente que se consulta en producción.
//
// El cliente no lleva Timeout propio: el límite de 10 segundos lo pone el
// contexto de cada petición (ver [Source.fetchCurrency]). Con los dos
// mecanismos activos el que venciera primero decidiría, y un Timeout de cliente
// no distingue "esta consulta se pasó" de "la invocación se está terminando",
// que es justamente la diferencia que la política de reintentos necesita ver.
func DefaultSource() Source {
	return Source{
		HTTPClient: &http.Client{},
		BaseURL:    ratesBaseURL,
	}
}

// withDefaults completa los campos que el llamador dejó sin definir.
func (s Source) withDefaults() Source {
	if s.HTTPClient == nil {
		s.HTTPClient = DefaultSource().HTTPClient
	}
	if s.BaseURL == "" {
		s.BaseURL = ratesBaseURL
	}
	return s
}

// FetchedRates son las tasas frescas de una consulta exitosa a la fuente
// externa, ya redondeadas.
//
// No es la respuesta del endpoint: no trae la marca de respaldo, porque unas
// tasas recién obtenidas no pueden ser un respaldo. Esa traducción la hace
// [FetchedRates.Snapshot].
type FetchedRates struct {
	// Date es la fecha informada por la fuente, en formato ISO 8601. Cuando las
	// dos divisas informan fechas distintas es la del USD (Requirement 14.12).
	Date string

	// UsdToClp es el valor del dólar en pesos, redondeado al entero más cercano
	// (Requirement 14.3).
	UsdToClp int64

	// BrlToClp es el valor del real en pesos, redondeado.
	BrlToClp int64

	// Attempts es la cantidad de intentos que hizo falta, contando el que
	// funcionó. Es el campo `attempts` del log de la invocación.
	Attempts int

	// UpstreamLatency es lo que tardó el intento exitoso, con las dos divisas
	// en paralelo. Excluye las esperas y los intentos fallidos que lo
	// precedieron: eso lo cuenta Attempts.
	UpstreamLatency time.Duration
}

// Snapshot traduce las tasas a la respuesta del endpoint, con la marca de
// respaldo desactivada (Requirement 14.5).
func (r FetchedRates) Snapshot() program.ExchangeSnapshot {
	return program.ExchangeSnapshot{
		Date:       r.Date,
		UsdToClp:   r.UsdToClp,
		BrlToClp:   r.BrlToClp,
		IsFallback: false,
	}
}

// UpstreamLatencyMs es la latencia en milisegundos, para el campo
// `upstreamLatencyMs` del log.
func (r FetchedRates) UpstreamLatencyMs() int64 {
	return r.UpstreamLatency.Milliseconds()
}

// FetchRates obtiene las tasas de USD y BRL a CLP de la fuente externa
// (Requirement 14.1).
//
// Cada intento consulta las dos divisas en paralelo con un límite de 10
// segundos por petición, y se repite hasta ratesMaxAttempts veces con espera
// creciente de factor 2 (Requirements 14.6 y 14.11). El intento que devuelve
// las dos cotizaciones termina la función; si los tres fallan, devuelve un
// UPSTREAM_SERVICE_ERROR con la última causa adjunta.
//
// El error se devuelve ya tipado como error de upstream para que el endpoint
// pueda entregarlo tal cual cuando además no exista snapshot de respaldo
// (Requirement 14.9). Esta función no lee ni escribe el snapshot: solo consulta
// la fuente.
//
// La cancelación del contexto corta el ciclo en cualquier punto, también
// durante la espera entre intentos. Es lo que hace que el timeout de la
// invocación gane siempre: sin eso, una Lambda a punto de vencer seguiría
// esperando un reintento que no va a alcanzar a hacer.
func FetchRates(ctx context.Context, source Source, log *zap.Logger) (FetchedRates, error) {
	source = source.withDefaults()

	var (
		lastErr  error
		attempts int
	)
	for attempt := 1; attempt <= ratesMaxAttempts; attempt++ {
		attempts = attempt
		startedAt := time.Now()

		rates, err := source.fetchOnce(ctx, log)
		if err == nil {
			rates.Attempts = attempt
			rates.UpstreamLatency = time.Since(startedAt)
			return rates, nil
		}

		lastErr = err
		log.Warn(attemptFailedEvent,
			zap.Int("attempt", attempt),
			zap.Int("maxAttempts", ratesMaxAttempts),
			zap.Error(err),
		)

		if attempt == ratesMaxAttempts {
			break
		}

		if waitErr := waitBeforeRetry(ctx, backoffFor(attempt)); waitErr != nil {
			lastErr = errors.Join(lastErr, waitErr)
			break
		}
	}

	return FetchedRates{Attempts: attempts}, apperr.
		UpstreamServiceError("No se pudieron obtener los tipos de cambio").
		WithCause(lastErr)
}

// fetchOnce es un intento completo: las dos divisas en paralelo, y las tasas ya
// redondeadas si las dos respondieron.
//
// Las dos peticiones comparten un contexto propio que se cancela en cuanto una
// falla. Sin eso, la divisa que sigue en vuelo consumiría sus 10 segundos para
// un resultado que ya no sirve: una respuesta sin USD o sin BRL es una consulta
// fallida completa, no una consulta a medias (Requirement 14.7).
func (s Source) fetchOnce(ctx context.Context, log *zap.Logger) (FetchedRates, error) {
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		usd, brl       currencyQuote
		usdErr, brlErr error
		wg             sync.WaitGroup
	)

	// Cada goroutine escribe en su propio par de variables, así que no hay
	// estado compartido que sincronizar más allá del Wait.
	fetch := func(currency string, quote *currencyQuote, quoteErr *error) {
		defer wg.Done()

		result, err := s.fetchCurrency(attemptCtx, currency)
		if err != nil {
			*quoteErr = err
			cancel()
			return
		}

		*quote = result
	}

	wg.Add(2)
	go fetch(currencyUSD, &usd, &usdErr)
	go fetch(currencyBRL, &brl, &brlErr)
	wg.Wait()

	if usdErr != nil || brlErr != nil {
		return FetchedRates{}, errors.Join(usdErr, brlErr)
	}

	return FetchedRates{
		Date:     resolveDate(usd, brl, log),
		UsdToClp: roundToClp(usd.toClp),
		BrlToClp: roundToClp(brl.toClp),
	}, nil
}

// currencyQuote es lo que se rescata del documento de una divisa: la fecha que
// la fuente informa y el valor de esa divisa en pesos, todavía sin redondear.
type currencyQuote struct {
	date  string
	toClp float64
}

// fetchCurrency consulta el documento de una divisa y devuelve su cotización.
//
// El límite de 10 segundos se aplica acá, sobre el contexto de esta petición
// (Requirement 14.11). Deriva del contexto del intento, así que la cancelación
// de la divisa hermana y el timeout de la invocación lo cortan antes de que
// venza.
func (s Source) fetchCurrency(ctx context.Context, currency string) (currencyQuote, error) {
	queryCtx, cancel := context.WithTimeout(ctx, ratesTimeout)
	defer cancel()

	url := fmt.Sprintf("%s/%s.json", s.BaseURL, currency)

	req, err := http.NewRequestWithContext(queryCtx, http.MethodGet, url, nil)
	if err != nil {
		return currencyQuote{}, fmt.Errorf("no se pudo armar la consulta de %s: %w", currency, err)
	}

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return currencyQuote{}, fmt.Errorf("la consulta de %s falló: %w", currency, err)
	}
	defer func() {
		// Se descarta lo que quede del cuerpo para que la conexión vuelva al
		// pool en vez de cerrarse: los seis pedidos del peor caso van al mismo
		// host.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return currencyQuote{}, fmt.Errorf("la fuente respondió %d para %s", resp.StatusCode, currency)
	}

	return decodeQuote(resp.Body, currency)
}

// decodeQuote interpreta el documento de una divisa.
//
// La forma es `{"date": "2026-03-14", "usd": {"clp": 947.31, ...}}`: la fecha y
// la divisa consultada comparten el nivel del cuerpo, y el nombre de la clave
// de la divisa depende de qué documento se pidió. Por eso se deserializa clave
// por clave y no en una struct fija.
//
// Ningún error de acá lleva la causa de encoding/json adjunta, y es deliberado:
// el mensaje de un error de sintaxis cita el fragmento del cuerpo donde
// apareció, y el cuerpo de la fuente externa no se registra. El caso de falla
// que importa distinguir —falta el valor en pesos— no depende de ese detalle.
func decodeQuote(body io.Reader, currency string) (currencyQuote, error) {
	var fields map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(body, maxBodyBytes)).Decode(&fields); err != nil {
		return currencyQuote{}, fmt.Errorf("el documento de %s no es un JSON interpretable", currency)
	}

	// Sin fecha no hay respuesta que armar: el contrato del endpoint la declara
	// obligatoria (Requirement 14.2). Se trata como consulta fallida por el
	// mismo criterio que un valor ausente, y así el respaldo —que sí tiene
	// fecha— entra en juego.
	rawDate, ok := fields[dateField]
	if !ok {
		return currencyQuote{}, fmt.Errorf("el documento de %s no informa fecha", currency)
	}

	var date string
	if err := json.Unmarshal(rawDate, &date); err != nil || date == "" {
		return currencyQuote{}, fmt.Errorf("la fecha del documento de %s no es utilizable", currency)
	}

	rawQuotes, ok := fields[currency]
	if !ok {
		return currencyQuote{}, fmt.Errorf("el documento no trae las cotizaciones de %s", currency)
	}

	var quotes map[string]float64
	if err := json.Unmarshal(rawQuotes, &quotes); err != nil {
		return currencyQuote{}, fmt.Errorf("las cotizaciones de %s no son interpretables", currency)
	}

	// Requirement 14.7. Un valor ausente y un valor no positivo se tratan
	// igual: con cualquiera de los dos el formulario convertiría todo a cero, y
	// una tasa de respaldo de ayer es mejor que un cero de hoy.
	value, ok := quotes[currencyCLP]
	if !ok || value <= 0 {
		return currencyQuote{}, fmt.Errorf("la fuente no informa el valor de %s en %s", currency, currencyCLP)
	}

	return currencyQuote{date: date, toClp: value}, nil
}

// resolveDate elige la fecha de la respuesta y advierte si las dos divisas no
// coinciden (Requirement 14.12, Propiedad 36).
//
// Siempre es la del USD, también cuando coinciden: es el único criterio que
// deja la fecha determinada sin depender de cuál de las dos peticiones terminó
// primero. La advertencia lleva las dos fechas para que el log muestre de qué
// tamaño fue la divergencia, que es lo que decide si vale seguirla.
func resolveDate(usd, brl currencyQuote, log *zap.Logger) string {
	if usd.date != brl.date {
		log.Warn(divergentDatesEvent,
			zap.String("usdDate", usd.date),
			zap.String("brlDate", brl.date),
		)
	}

	return usd.date
}

// roundToClp redondea una tasa al entero más cercano (Requirement 14.3,
// Propiedad 33).
//
// math.Round y no int64(rate + 0.5): el segundo redondea hacia arriba en el
// medio y hacia el cero en los negativos, así que no es "al entero más
// cercano" en todo el dominio. Una tasa negativa no llega hasta acá —
// decodeQuote ya rechaza los valores no positivos— pero la función se lee sola
// y no depende de esa garantía externa para ser correcta.
func roundToClp(rate float64) int64 {
	return int64(math.Round(rate))
}

// backoffFor es la espera antes del intento siguiente al indicado: 500 ms
// después del primero y 1 s después del segundo.
func backoffFor(attempt int) time.Duration {
	wait := ratesBackoff
	for i := 1; i < attempt; i++ {
		wait *= ratesBackoffFactor
	}

	return wait
}

// waitBeforeRetry espera lo indicado, o corta antes si el contexto se cancela.
//
// Un time.Sleep pelado ignoraría el timeout de la invocación y se quedaría
// esperando un reintento que la Lambda ya no va a alcanzar a completar.
func waitBeforeRetry(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
