package actualizarfavoritov1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
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
	testTableName  = "ind-dev-favoritos-ddb-dev-pri-use1"
	testUserID     = "01JCTXUSER0000000000000000"
	testRequestID  = "01JREQ00000000000000000000"
	testFavoriteID = "01JQZ8CCCCCCCCCCCCCCCCCCCC"
)

type fakeDDB struct {
	t            *testing.T
	updateInput  *dynamodb.UpdateItemInput
	updateOutput *dynamodb.UpdateItemOutput
	updateErr    error
}

func (f *fakeDDB) UpdateItem(
	ctx context.Context,
	params *dynamodb.UpdateItemInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.UpdateItemOutput, error) {
	f.t.Helper()
	if ctx == nil {
		f.t.Fatal("UpdateItem recibió un contexto nil")
	}
	f.updateInput = params

	return f.updateOutput, f.updateErr
}

func (f *fakeDDB) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.t.Fatal("actualizar un favorito no debe usar GetItem")
	return nil, nil
}

func (f *fakeDDB) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.t.Fatal("actualizar un favorito no debe usar Query")
	return nil, nil
}

func (f *fakeDDB) PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.t.Fatal("actualizar un favorito no debe usar PutItem")
	return nil, nil
}

func (f *fakeDDB) DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	f.t.Fatal("actualizar un favorito no debe usar DeleteItem")
	return nil, nil
}

func testApp(ddb *fakeDDB) *functions.App {
	return &functions.App{
		Config: functions.Config{
			FavoritesTableName: testTableName,
			FunctionName:       "fn-actualizar-favorito-v1",
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
		Name: "Programa actualizado",
		Content: domain.FavoriteContent{
			Generals: program.ProgramGeneral{
				Name:          "Gira actualizada",
				Plan:          program.CatalogRef{ID: "plan", Display: "Gira de estudio"},
				Season:        program.CatalogRef{ID: "2027", Display: "2027"},
				Destination:   program.CatalogRef{ID: "BRF", Display: "Florianópolis"},
				DepartureCity: "Santiago",
			},
			Schedule: program.ScheduleContent{
				TotalDays:       7,
				TotalNights:     5,
				TotalPassengers: 35,
				FreePassengers:  2,
			},
			Pricing: program.PricingContent{UtilityRate: 20, RechargeRate: 5},
		},
	}
}

func apiRequest(t *testing.T, request Request, favoriteID string) events.APIGatewayV2HTTPRequest {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	return events.APIGatewayV2HTTPRequest{
		Body:           string(body),
		PathParameters: map[string]string{functions.FavoriteIDParam: favoriteID},
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			RequestID: testRequestID,
			Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
				Lambda: map[string]any{lambdautil.AuthorizerUserIDKey: testUserID},
			},
		},
	}
}

func updatedItem(t *testing.T, request Request) map[string]types.AttributeValue {
	t.Helper()
	createdAt := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	item, err := domain.NewFavoriteItem(
		domain.FavoriteKey{UserID: testUserID, Scope: domain.ScopeProgram, ID: testFavoriteID},
		request.Name,
		request.Content,
		createdAt,
		updatedAt,
	)
	if err != nil {
		t.Fatalf("NewFavoriteItem() error = %v", err)
	}
	raw, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatalf("attributevalue.MarshalMap() error = %v", err)
	}

	return raw
}

func decodeError(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return envelope.Code
}

func TestHandleUpdatesExistingFavorite(t *testing.T) {
	request := validRequest()
	ddb := &fakeDDB{
		t:            t,
		updateOutput: &dynamodb.UpdateItemOutput{Attributes: updatedItem(t, request)},
	}
	log, logs := testLogger()

	response, err := handle(context.Background(), testApp(ddb), log, apiRequest(t, request, testFavoriteID))
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want %d — %s", response.StatusCode, http.StatusOK, response.Body)
	}
	if ddb.updateInput == nil {
		t.Fatal("UpdateItem no fue invocado")
	}
	if got := aws.ToString(ddb.updateInput.TableName); got != testTableName {
		t.Errorf("TableName = %q, want %q", got, testTableName)
	}
	if ddb.updateInput.ReturnValues != types.ReturnValueAllNew {
		t.Errorf("ReturnValues = %q, want ALL_NEW", ddb.updateInput.ReturnValues)
	}
	if got := aws.ToString(ddb.updateInput.ConditionExpression); got != "attribute_exists(pk) AND attribute_exists(sk)" {
		t.Errorf("ConditionExpression = %q", got)
	}

	var key domain.Key
	if err := attributevalue.UnmarshalMap(ddb.updateInput.Key, &key); err != nil {
		t.Fatalf("no se pudo leer la clave: %v", err)
	}
	if key.PK != domain.UserPKPrefix+testUserID || key.SK != domain.ScopeProgram.SKPrefix()+testFavoriteID {
		t.Errorf("Key = %+v, want usuario del contexto e id del path", key)
	}

	var envelope struct {
		Data domain.Favorite `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
		t.Fatalf("respuesta inválida: %v", err)
	}
	if envelope.Data.ID != testFavoriteID || envelope.Data.Name != request.Name {
		t.Errorf("favorito devuelto = %+v", envelope.Data)
	}

	entries := logs.FilterMessage("favoriteUpdated").All()
	if len(entries) != 1 {
		t.Fatalf("logs favoriteUpdated = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["favoriteId"] != testFavoriteID || fields["userId"] != testUserID || fields["operation"] != operationName {
		t.Errorf("campos del log = %+v", fields)
	}
}

func TestHandleReturnsNotFoundWhenFavoriteDoesNotExist(t *testing.T) {
	ddb := &fakeDDB{
		t: t,
		updateErr: &types.ConditionalCheckFailedException{
			Message: aws.String("condition failed"),
		},
	}
	log, _ := testLogger()

	response, err := handle(context.Background(), testApp(ddb), log, apiRequest(t, validRequest(), testFavoriteID))
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if response.StatusCode != http.StatusNotFound || decodeError(t, response.Body) != apperr.CodeResourceNotFound {
		t.Errorf("respuesta = (%d, %s), want RESOURCE_NOT_FOUND", response.StatusCode, response.Body)
	}
}

func TestHandleRejectsInvalidIDAndContentBeforeUpdating(t *testing.T) {
	tests := []struct {
		name       string
		favoriteID string
		request    Request
		wantCode   string
	}{
		{
			name:       "id inválido",
			favoriteID: "no-es-ulid",
			request:    validRequest(),
			wantCode:   apperr.CodeValidationError,
		},
		{
			name:       "nombre ausente",
			favoriteID: testFavoriteID,
			request: func() Request {
				request := validRequest()
				request.Name = " "
				return request
			}(),
			wantCode: apperr.CodeRequiredFieldMissing,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ddb := &fakeDDB{t: t}
			log, _ := testLogger()
			response, err := handle(context.Background(), testApp(ddb), log, apiRequest(t, tc.request, tc.favoriteID))
			if err != nil {
				t.Fatalf("handle() error = %v", err)
			}
			if response.StatusCode != http.StatusBadRequest || decodeError(t, response.Body) != tc.wantCode {
				t.Errorf("respuesta = (%d, %s), want %s", response.StatusCode, response.Body, tc.wantCode)
			}
			if ddb.updateInput != nil {
				t.Error("la solicitud inválida alcanzó UpdateItem")
			}
		})
	}
}

func TestHandleReturnsInternalErrorWhenUpdateFails(t *testing.T) {
	ddb := &fakeDDB{t: t, updateErr: errors.New("dynamodb unavailable")}
	log, _ := testLogger()

	response, err := handle(context.Background(), testApp(ddb), log, apiRequest(t, validRequest(), testFavoriteID))
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if response.StatusCode != http.StatusInternalServerError || decodeError(t, response.Body) != apperr.CodeInternalError {
		t.Errorf("respuesta = (%d, %s), want INTERNAL_ERROR", response.StatusCode, response.Body)
	}
}
