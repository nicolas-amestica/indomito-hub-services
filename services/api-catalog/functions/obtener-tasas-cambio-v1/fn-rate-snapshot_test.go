package rates

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"

	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions"
)

const snapshotTestTable = "catalogos-test"

type rateDDB struct {
	t *testing.T

	getOutput *dynamodb.GetItemOutput
	getErr    error
	putErr    error

	getInputs []*dynamodb.GetItemInput
	putInputs []*dynamodb.PutItemInput
}

var _ awsddb.Client = (*rateDDB)(nil)

func (f *rateDDB) GetItem(
	_ context.Context,
	params *dynamodb.GetItemInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.GetItemOutput, error) {
	f.getInputs = append(f.getInputs, params)
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getOutput == nil {
		return &dynamodb.GetItemOutput{}, nil
	}
	return f.getOutput, nil
}

func (f *rateDDB) PutItem(
	_ context.Context,
	params *dynamodb.PutItemInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.PutItemOutput, error) {
	f.putInputs = append(f.putInputs, params)
	if f.putErr != nil {
		return nil, f.putErr
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (f *rateDDB) Query(
	context.Context,
	*dynamodb.QueryInput,
	...func(*dynamodb.Options),
) (*dynamodb.QueryOutput, error) {
	f.t.Fatal("el endpoint de tasas no debe usar Query")
	return nil, nil
}

func (f *rateDDB) UpdateItem(
	context.Context,
	*dynamodb.UpdateItemInput,
	...func(*dynamodb.Options),
) (*dynamodb.UpdateItemOutput, error) {
	f.t.Fatal("el endpoint de tasas no debe usar UpdateItem")
	return nil, nil
}

func (f *rateDDB) DeleteItem(
	context.Context,
	*dynamodb.DeleteItemInput,
	...func(*dynamodb.Options),
) (*dynamodb.DeleteItemOutput, error) {
	f.t.Fatal("el endpoint de tasas no debe usar DeleteItem")
	return nil, nil
}

type ratesEnvelope struct {
	Data    *program.ExchangeSnapshot `json:"data"`
	Code    string                    `json:"code"`
	TraceID string                    `json:"traceId"`
}

func marshalSnapshotItem(t *testing.T, item domain.RateItem) map[string]ddbtypes.AttributeValue {
	t.Helper()

	attributes, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatalf("no se pudo serializar el snapshot de prueba: %v", err)
	}
	return attributes
}

func decodeRatesEnvelope(t *testing.T, response events.APIGatewayV2HTTPResponse) ratesEnvelope {
	t.Helper()

	var envelope ratesEnvelope
	if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
		t.Fatalf("la respuesta no es JSON: %v", err)
	}
	return envelope
}

func rateObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

func TestReadRateSnapshotUsesTheCompleteKey(t *testing.T) {
	fetchedAt := time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)
	want := domain.NewRateItem(program.ExchangeSnapshot{
		Date: "2026-03-14", UsdToClp: 950, BrlToClp: 165,
	}, fetchedAt)
	ddb := &rateDDB{
		t:         t,
		getOutput: &dynamodb.GetItemOutput{Item: marshalSnapshotItem(t, want)},
	}

	got, found, err := ReadRateSnapshot(context.Background(), ddb, snapshotTestTable)
	if err != nil {
		t.Fatalf("ReadRateSnapshot devolvio error: %v", err)
	}
	if !found {
		t.Fatal("ReadRateSnapshot no encontro el item configurado")
	}
	if got.Date != want.Date || got.UsdToClp != want.UsdToClp || got.BrlToClp != want.BrlToClp ||
		!got.FetchedAt.Equal(want.FetchedAt) {
		t.Errorf("snapshot = %+v, se esperaba %+v", got, want)
	}

	if len(ddb.getInputs) != 1 {
		t.Fatalf("se hicieron %d lecturas, se esperaba una", len(ddb.getInputs))
	}
	input := ddb.getInputs[0]
	if input.TableName == nil || *input.TableName != snapshotTestTable {
		t.Errorf("la lectura apunta a %v", input.TableName)
	}
	if input.ConsistentRead == nil || !*input.ConsistentRead {
		t.Error("la lectura del ultimo snapshot debe ser consistente")
	}

	var key domain.Key
	if err := attributevalue.UnmarshalMap(input.Key, &key); err != nil {
		t.Fatalf("la clave de lectura no se pudo interpretar: %v", err)
	}
	if key != domain.RatesSnapshotKey() {
		t.Errorf("clave = %+v, se esperaba %+v", key, domain.RatesSnapshotKey())
	}
}

func TestReadRateSnapshotDistinguishesAbsenceFromFailure(t *testing.T) {
	tests := []struct {
		name    string
		ddb     *rateDDB
		wantErr bool
	}{
		{
			name: "item ausente",
			ddb:  &rateDDB{t: t, getOutput: &dynamodb.GetItemOutput{}},
		},
		{
			name:    "falla de DynamoDB",
			ddb:     &rateDDB{t: t, getErr: errors.New("ddb no disponible")},
			wantErr: true,
		},
		{
			name: "item incompleto",
			ddb: &rateDDB{t: t, getOutput: &dynamodb.GetItemOutput{Item: marshalSnapshotItem(t, domain.RateItem{
				PK: domain.RatesPK, SK: domain.RatesLatestSK, Date: "2026-03-14",
			})}},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, found, err := ReadRateSnapshot(context.Background(), test.ddb, snapshotTestTable)
			if found {
				t.Error("un item ausente o invalido no puede informarse como encontrado")
			}
			if (err != nil) != test.wantErr {
				t.Errorf("error = %v, wantErr = %t", err, test.wantErr)
			}
			if err != nil && apperr.From(err).Code() != apperr.CodeInternalError {
				t.Errorf("code = %q, se esperaba INTERNAL_ERROR", apperr.From(err).Code())
			}
		})
	}
}

func TestServeRatesUsesFreshValuesOrFallback(t *testing.T) {
	fetchedAt := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)
	fresh := FetchedRates{
		Date: "2026-03-14", UsdToClp: 950, BrlToClp: 165,
		Attempts: 2, UpstreamLatency: 125 * time.Millisecond,
	}
	fallbackItem := domain.NewRateItem(program.ExchangeSnapshot{
		Date: "2026-03-13", UsdToClp: 945, BrlToClp: 163,
	}, fetchedAt.Add(-24*time.Hour))
	upstreamErr := apperr.UpstreamServiceError("No se pudieron obtener los tipos de cambio")

	tests := []struct {
		name         string
		fetched      FetchedRates
		fetchErr     error
		getOutput    *dynamodb.GetItemOutput
		getErr       error
		putErr       error
		wantStatus   int
		wantFallback *bool
		wantCode     string
		wantGets     int
		wantPuts     int
		wantLogEvent string
	}{
		{
			name:         "tasas frescas escriben snapshot",
			fetched:      fresh,
			wantStatus:   http.StatusOK,
			wantFallback: boolPointer(false),
			wantPuts:     1,
			wantLogEvent: ratesResolvedEvent,
		},
		{
			name:         "falla de escritura conserva respuesta fresca",
			fetched:      fresh,
			putErr:       errors.New("escritura rechazada"),
			wantStatus:   http.StatusOK,
			wantFallback: boolPointer(false),
			wantPuts:     1,
			wantLogEvent: snapshotWriteErrorEvent,
		},
		{
			name:         "fuente caida entrega snapshot",
			fetched:      FetchedRates{Attempts: ratesMaxAttempts},
			fetchErr:     upstreamErr,
			getOutput:    &dynamodb.GetItemOutput{Item: marshalSnapshotItem(t, fallbackItem)},
			wantStatus:   http.StatusOK,
			wantFallback: boolPointer(true),
			wantGets:     1,
			wantLogEvent: fallbackDeliveredEvent,
		},
		{
			name:         "fuente caida sin snapshot responde upstream",
			fetched:      FetchedRates{Attempts: ratesMaxAttempts},
			fetchErr:     upstreamErr,
			getOutput:    &dynamodb.GetItemOutput{},
			wantStatus:   http.StatusBadGateway,
			wantCode:     apperr.CodeUpstreamServiceError,
			wantGets:     1,
			wantLogEvent: ratesUnavailableEvent,
		},
		{
			name:         "falla al leer snapshot responde error interno",
			fetched:      FetchedRates{Attempts: ratesMaxAttempts},
			fetchErr:     upstreamErr,
			getErr:       errors.New("lectura rechazada"),
			wantStatus:   http.StatusInternalServerError,
			wantCode:     apperr.CodeInternalError,
			wantGets:     1,
			wantLogEvent: snapshotReadErrorEvent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ddb := &rateDDB{
				t:         t,
				getOutput: test.getOutput,
				getErr:    test.getErr,
				putErr:    test.putErr,
			}
			app := &functions.App{
				Config: functions.Config{CatalogsTableName: snapshotTestTable},
				DDB:    ddb,
				Stage:  "test",
			}
			req := events.APIGatewayV2HTTPRequest{
				RequestContext: events.APIGatewayV2HTTPRequestContext{RequestID: "trace-rates"},
			}
			fetch := func(context.Context, Source, *zap.Logger) (FetchedRates, error) {
				return test.fetched, test.fetchErr
			}
			log, logs := rateObservedLogger()

			response, err := serveRates(
				context.Background(), app, req, Source{}, fetch, func() time.Time { return fetchedAt }, log,
			)
			if err != nil {
				t.Fatalf("serveRates devolvio error Lambda: %v", err)
			}
			if response.StatusCode != test.wantStatus {
				t.Errorf("status = %d, se esperaba %d", response.StatusCode, test.wantStatus)
			}

			envelope := decodeRatesEnvelope(t, response)
			if test.wantFallback != nil {
				if envelope.Data == nil {
					t.Fatal("la respuesta exitosa no contiene data")
				}
				if envelope.Data.IsFallback != *test.wantFallback {
					t.Errorf("isFallback = %t, se esperaba %t", envelope.Data.IsFallback, *test.wantFallback)
				}
			}
			if envelope.Code != test.wantCode {
				t.Errorf("code = %q, se esperaba %q", envelope.Code, test.wantCode)
			}
			if test.wantCode != "" && envelope.TraceID != "trace-rates" {
				t.Errorf("traceId = %q", envelope.TraceID)
			}
			if len(ddb.getInputs) != test.wantGets {
				t.Errorf("GetItem se llamo %d veces, se esperaba %d", len(ddb.getInputs), test.wantGets)
			}
			if len(ddb.putInputs) != test.wantPuts {
				t.Errorf("PutItem se llamo %d veces, se esperaba %d", len(ddb.putInputs), test.wantPuts)
			}
			if logs.FilterMessage(test.wantLogEvent).Len() != 1 {
				t.Errorf("el evento %q se registro %d veces", test.wantLogEvent, logs.FilterMessage(test.wantLogEvent).Len())
			}

			if test.wantPuts == 1 {
				assertWrittenSnapshot(t, ddb.putInputs[0], fresh.Snapshot(), fetchedAt)
			}
			if test.wantFallback != nil && *test.wantFallback {
				if envelope.Data.Date != fallbackItem.Date || envelope.Data.UsdToClp != fallbackItem.UsdToClp ||
					envelope.Data.BrlToClp != fallbackItem.BrlToClp {
					t.Errorf("fallback = %+v, se esperaba %+v", envelope.Data, fallbackItem)
				}
			}
		})
	}
}

func TestFreshAndFallbackSnapshotProperty(t *testing.T) {
	t.Run("Feature: program-form, Property 34: Una consulta exitosa deja snapshot sin marca, y una fallida lo entrega marcado", func(t *testing.T) {
		fetchedAt := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)
		fresh := FetchedRates{
			Date: "2026-03-14", UsdToClp: 950, BrlToClp: 165, Attempts: 1,
		}
		stored := domain.NewRateItem(program.ExchangeSnapshot{
			Date: "2026-03-13", UsdToClp: 945, BrlToClp: 163,
		}, fetchedAt.Add(-24*time.Hour))

		tests := []struct {
			name         string
			fetched      FetchedRates
			fetchErr     error
			getOutput    *dynamodb.GetItemOutput
			wantFallback bool
			wantPuts     int
		}{
			{
				name:     "consulta exitosa",
				fetched:  fresh,
				wantPuts: 1,
			},
			{
				name:         "tres intentos fallidos con snapshot",
				fetched:      FetchedRates{Attempts: ratesMaxAttempts},
				fetchErr:     apperr.UpstreamServiceError("fuente no disponible"),
				getOutput:    &dynamodb.GetItemOutput{Item: marshalSnapshotItem(t, stored)},
				wantFallback: true,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				ddb := &rateDDB{t: t, getOutput: test.getOutput}
				app := &functions.App{
					Config: functions.Config{CatalogsTableName: snapshotTestTable},
					DDB:    ddb,
					Stage:  "test",
				}
				fetch := func(context.Context, Source, *zap.Logger) (FetchedRates, error) {
					return test.fetched, test.fetchErr
				}

				response, err := serveRates(
					context.Background(), app, events.APIGatewayV2HTTPRequest{}, Source{}, fetch,
					func() time.Time { return fetchedAt }, zap.NewNop(),
				)
				if err != nil {
					t.Fatalf("serveRates devolvio error Lambda: %v", err)
				}
				if response.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, se esperaba 200", response.StatusCode)
				}

				envelope := decodeRatesEnvelope(t, response)
				if envelope.Data == nil {
					t.Fatal("la respuesta no contiene el snapshot")
				}
				if envelope.Data.IsFallback != test.wantFallback {
					t.Errorf("isFallback = %t, se esperaba %t", envelope.Data.IsFallback, test.wantFallback)
				}
				if len(ddb.putInputs) != test.wantPuts {
					t.Errorf("PutItem se llamo %d veces, se esperaba %d", len(ddb.putInputs), test.wantPuts)
				}
			})
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func sourceWithDocuments(usdDocument, brlDocument string) Source {
	return Source{
		BaseURL: "https://rates.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			document := usdDocument
			if strings.HasSuffix(req.URL.Path, "/brl.json") {
				document = brlDocument
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(document)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}
}

func TestUnusableSourceWithoutSnapshotProperty(t *testing.T) {
	t.Run("Feature: program-form, Property 35: Una respuesta externa inutilizable sin snapshot produce un error de upstream", func(t *testing.T) {
		validUSD := `{"date":"2026-03-14","usd":{"clp":950.2}}`
		validBRL := `{"date":"2026-03-14","brl":{"clp":165.4}}`
		tests := []struct {
			name string
			usd  string
			brl  string
		}{
			{name: "USD ausente", usd: `{"date":"2026-03-14","usd":{}}`, brl: validBRL},
			{name: "BRL ausente", usd: validUSD, brl: `{"date":"2026-03-14","brl":{}}`},
			{name: "USD no numerico", usd: `{"date":"2026-03-14","usd":{"clp":"950"}}`, brl: validBRL},
			{name: "BRL no numerico", usd: validUSD, brl: `{"date":"2026-03-14","brl":{"clp":"165"}}`},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				ddb := &rateDDB{t: t, getOutput: &dynamodb.GetItemOutput{}}
				app := &functions.App{
					Config: functions.Config{CatalogsTableName: snapshotTestTable},
					DDB:    ddb,
					Stage:  "test",
				}
				req := events.APIGatewayV2HTTPRequest{
					RequestContext: events.APIGatewayV2HTTPRequestContext{RequestID: "trace-property-35"},
				}

				response, err := serveRates(
					context.Background(), app, req, sourceWithDocuments(test.usd, test.brl), FetchRates,
					time.Now, zap.NewNop(),
				)
				if err != nil {
					t.Fatalf("serveRates devolvio error Lambda: %v", err)
				}
				if response.StatusCode != http.StatusBadGateway {
					t.Errorf("status = %d, se esperaba 502", response.StatusCode)
				}
				envelope := decodeRatesEnvelope(t, response)
				if envelope.Code != apperr.CodeUpstreamServiceError {
					t.Errorf("code = %q, se esperaba %q", envelope.Code, apperr.CodeUpstreamServiceError)
				}
				if envelope.TraceID != "trace-property-35" {
					t.Errorf("traceId = %q", envelope.TraceID)
				}
				if len(ddb.getInputs) != 1 {
					t.Errorf("GetItem se llamo %d veces, se esperaba una", len(ddb.getInputs))
				}
			})
		}
	})
}

func boolPointer(value bool) *bool {
	return &value
}

func assertWrittenSnapshot(
	t *testing.T,
	input *dynamodb.PutItemInput,
	want program.ExchangeSnapshot,
	wantFetchedAt time.Time,
) {
	t.Helper()

	if input.TableName == nil || *input.TableName != snapshotTestTable {
		t.Errorf("la escritura apunta a %v", input.TableName)
	}

	var item domain.RateItem
	if err := attributevalue.UnmarshalMap(input.Item, &item); err != nil {
		t.Fatalf("el item escrito no se pudo interpretar: %v", err)
	}
	if item.PK != domain.RatesPK || item.SK != domain.RatesLatestSK ||
		item.Date != want.Date || item.UsdToClp != want.UsdToClp || item.BrlToClp != want.BrlToClp ||
		!item.FetchedAt.Equal(wantFetchedAt) {
		t.Errorf("item escrito = %+v", item)
	}
}
