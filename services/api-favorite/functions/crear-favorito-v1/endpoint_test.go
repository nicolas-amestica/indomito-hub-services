package crearfavoritov1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-favorite/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-favorite/functions"
)

const (
	testTableName = "ind-dev-favoritos-ddb-dev-pri-use1"
	testUserID    = "01JCTXUSER0000000000000000"
	testRequestID = "01JREQ00000000000000000000"
)

type fakeDDB struct {
	t        *testing.T
	putInput *dynamodb.PutItemInput
	putErr   error
}

func (f *fakeDDB) PutItem(
	ctx context.Context,
	params *dynamodb.PutItemInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.PutItemOutput, error) {
	f.t.Helper()
	if ctx == nil {
		f.t.Fatal("PutItem recibió un contexto nil")
	}
	f.putInput = params

	return &dynamodb.PutItemOutput{}, f.putErr
}

func (f *fakeDDB) GetItem(
	context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options),
) (*dynamodb.GetItemOutput, error) {
	f.t.Fatal("crear un favorito no debe usar GetItem")

	return nil, nil
}

func (f *fakeDDB) Query(
	context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options),
) (*dynamodb.QueryOutput, error) {
	f.t.Fatal("crear un favorito no debe usar Query")

	return nil, nil
}

func (f *fakeDDB) UpdateItem(
	context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options),
) (*dynamodb.UpdateItemOutput, error) {
	f.t.Fatal("crear un favorito no debe usar UpdateItem")

	return nil, nil
}

func (f *fakeDDB) DeleteItem(
	context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options),
) (*dynamodb.DeleteItemOutput, error) {
	f.t.Fatal("crear un favorito no debe usar DeleteItem")

	return nil, nil
}

func testApp(ddb *fakeDDB) *functions.App {
	return &functions.App{
		Config: functions.Config{
			FavoritesTableName: testTableName,
			FunctionName:       "fn-crear-favorito-v1",
			Port:               "8082",
		},
		DDB:   ddb,
		Stage: "test",
	}
}

func testLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)

	return zap.New(core), logs
}

func validRequest() Request {
	return Request{
		Name: "Programa Florianópolis",
		Content: domain.FavoriteContent{
			Generals: program.ProgramGeneral{
				Name: "Gira Cuarto Medio",
				Plan: program.CatalogRef{
					ID:      "01M2ET1GWJQ9YHHCJP7WQR9RNH",
					Display: "Gira de estudio",
				},
				Season: program.CatalogRef{ID: "2027", Display: "2027"},
				Destination: program.CatalogRef{
					ID:               "BRF",
					Display:          "Florianópolis",
					BudgetTemplateID: "brochure-default",
				},
				DepartureCity: "Santiago",
			},
			Schedule: program.ScheduleContent{
				TotalDays:       7,
				TotalNights:     5,
				TotalPassengers: 35,
				FreePassengers:  2,
			},
			Pricing: program.PricingContent{
				UsdIncreaseCLP: 50,
				BrlIncreaseCLP: 10,
				UtilityRate:    20,
				RechargeRate:   5,
			},
			Crews: []program.CrewMember{{
				Name:       "Coordinador",
				DocumentID: "12345678",
				DailyPrice: 45000,
				Currency:   program.CurrencyCLP,
			}},
			Services: []program.ProgramService{{
				Name:       "Hotel",
				ChargeType: program.ChargePerPassengerNight,
				UnitPrice:  55,
				Currency:   program.CurrencyUSD,
			}},
		},
	}
}

func apiRequest(t *testing.T, body any) events.APIGatewayV2HTTPRequest {
	t.Helper()

	rawBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	return events.APIGatewayV2HTTPRequest{
		RawPath: "/favoritos",
		Body:    string(rawBody),
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			RequestID: testRequestID,
			Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
				Lambda: map[string]any{lambdautil.AuthorizerUserIDKey: testUserID},
			},
		},
	}
}

func decodeFavorite(t *testing.T, body string) domain.Favorite {
	t.Helper()

	var envelope struct {
		Data domain.Favorite `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("la respuesta no es el envelope de éxito esperado: %v — %s", err, body)
	}

	return envelope.Data
}

func decodeError(t *testing.T, body string) (code string, traceID string) {
	t.Helper()

	var envelope struct {
		Code    string `json:"code"`
		TraceID string `json:"traceId"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("la respuesta no es el envelope de error esperado: %v — %s", err, body)
	}

	return envelope.Code, envelope.TraceID
}

func TestHandleCreatesFavoriteForAuthenticatedUser(t *testing.T) {
	ddb := &fakeDDB{t: t}
	log, logs := testLogger()
	request := validRequest()
	request.Name = "  " + request.Name + "  "

	response, err := handle(context.Background(), testApp(ddb), log, apiRequest(t, request))
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("StatusCode = %d, want %d — %s", response.StatusCode, http.StatusCreated, response.Body)
	}
	if ddb.putInput == nil {
		t.Fatal("PutItem no fue invocado")
	}
	if got := aws.ToString(ddb.putInput.TableName); got != testTableName {
		t.Errorf("TableName = %q, want %q", got, testTableName)
	}
	if got := aws.ToString(ddb.putInput.ConditionExpression); got != "attribute_not_exists(pk) AND attribute_not_exists(sk)" {
		t.Errorf("ConditionExpression = %q", got)
	}

	var stored domain.FavoriteItem
	if err := attributevalue.UnmarshalMap(ddb.putInput.Item, &stored); err != nil {
		t.Fatalf("no se pudo leer el ítem escrito: %v", err)
	}
	if stored.PK != domain.UserPKPrefix+testUserID {
		t.Errorf("PK = %q, want usuario del authorizer", stored.PK)
	}
	if stored.Name != "Programa Florianópolis" {
		t.Errorf("Name = %q, want nombre recortado", stored.Name)
	}
	if !stringsHasPrefix(stored.SK, domain.ScopeQuotation.SKPrefix()) {
		t.Errorf("SK = %q, want prefijo %q", stored.SK, domain.ScopeQuotation.SKPrefix())
	}
	if stored.CreatedAt.IsZero() || !stored.CreatedAt.Equal(stored.UpdatedAt) {
		t.Errorf("fechas escritas = (%s, %s), want iguales y no cero", stored.CreatedAt, stored.UpdatedAt)
	}

	favorite := decodeFavorite(t, response.Body)
	if favorite.ID == "" || domain.ScopeQuotation.SKPrefix()+favorite.ID != stored.SK {
		t.Errorf("favorite.ID = %q, no corresponde a SK %q", favorite.ID, stored.SK)
	}
	if favorite.Scope != domain.ScopeQuotation || favorite.Name != stored.Name {
		t.Errorf("favorito devuelto = %+v, want scope y nombre persistidos", favorite)
	}

	created := logs.FilterMessage("favoriteCreated").All()
	if len(created) != 1 {
		t.Fatalf("logs favoriteCreated = %d, want 1", len(created))
	}
	fields := created[0].ContextMap()
	if fields["favoriteId"] != favorite.ID || fields["userId"] != testUserID || fields["operation"] != operationName {
		t.Errorf("campos del log = %+v", fields)
	}
	for _, forbidden := range []string{"name", "content", "documentId"} {
		if _, present := fields[forbidden]; present {
			t.Errorf("el log expuso el campo sensible %q", forbidden)
		}
	}
}

func TestHandleRejectsInvalidRequestsBeforeWriting(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*events.APIGatewayV2HTTPRequest)
		body       any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "cuerpo vacío",
			body:       validRequest(),
			mutate:     func(req *events.APIGatewayV2HTTPRequest) { req.Body = "" },
			wantStatus: http.StatusBadRequest,
			wantCode:   apperr.CodeInvalidRequestFormat,
		},
		{
			name: "nombre ausente",
			body: func() Request {
				request := validRequest()
				request.Name = "   "
				return request
			}(),
			wantStatus: http.StatusBadRequest,
			wantCode:   apperr.CodeRequiredFieldMissing,
		},
		{
			name: "contenido fuera de límites",
			body: func() Request {
				request := validRequest()
				request.Content.Schedule.TotalPassengers = 101
				return request
			}(),
			wantStatus: http.StatusBadRequest,
			wantCode:   apperr.CodeValidationError,
		},
		{
			name: "identidad ausente",
			body: validRequest(),
			mutate: func(req *events.APIGatewayV2HTTPRequest) {
				req.RequestContext.Authorizer = nil
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   apperr.CodeUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ddb := &fakeDDB{t: t}
			log, _ := testLogger()
			req := apiRequest(t, tc.body)
			if tc.mutate != nil {
				tc.mutate(&req)
			}

			response, err := handle(context.Background(), testApp(ddb), log, req)
			if err != nil {
				t.Fatalf("handle() error = %v", err)
			}
			if response.StatusCode != tc.wantStatus {
				t.Fatalf("StatusCode = %d, want %d — %s", response.StatusCode, tc.wantStatus, response.Body)
			}
			code, traceID := decodeError(t, response.Body)
			if code != tc.wantCode || traceID != testRequestID {
				t.Errorf("error = (%q, %q), want (%q, %q)", code, traceID, tc.wantCode, testRequestID)
			}
			if ddb.putInput != nil {
				t.Error("la solicitud inválida alcanzó PutItem")
			}
		})
	}
}

func TestHandleReturnsInternalErrorWhenPutFails(t *testing.T) {
	ddb := &fakeDDB{t: t, putErr: errors.New("dynamodb unavailable")}
	log, logs := testLogger()

	response, err := handle(context.Background(), testApp(ddb), log, apiRequest(t, validRequest()))
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("StatusCode = %d, want %d — %s", response.StatusCode, http.StatusInternalServerError, response.Body)
	}
	if code, _ := decodeError(t, response.Body); code != apperr.CodeInternalError {
		t.Errorf("code = %q, want %q", code, apperr.CodeInternalError)
	}

	failed := logs.FilterMessage("createFavoriteFailed").All()
	if len(failed) != 1 {
		t.Fatalf("logs createFavoriteFailed = %d, want 1", len(failed))
	}
	fields := failed[0].ContextMap()
	if fields["userId"] != testUserID || fields["operation"] != operationName || fields["favoriteId"] == "" {
		t.Errorf("campos del log = %+v", fields)
	}
}

func stringsHasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
