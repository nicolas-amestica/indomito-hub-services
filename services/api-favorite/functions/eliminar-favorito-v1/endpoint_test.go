package eliminarfavoritov1

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
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

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
	t           *testing.T
	deleteInput *dynamodb.DeleteItemInput
	deleteErr   error
}

func (f *fakeDDB) DeleteItem(
	ctx context.Context,
	params *dynamodb.DeleteItemInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.DeleteItemOutput, error) {
	f.t.Helper()
	if ctx == nil {
		f.t.Fatal("DeleteItem recibió un contexto nil")
	}
	f.deleteInput = params

	return &dynamodb.DeleteItemOutput{}, f.deleteErr
}

func (f *fakeDDB) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.t.Fatal("eliminar un favorito no debe usar GetItem")
	return nil, nil
}

func (f *fakeDDB) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.t.Fatal("eliminar un favorito no debe usar Query")
	return nil, nil
}

func (f *fakeDDB) PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.t.Fatal("eliminar un favorito no debe usar PutItem")
	return nil, nil
}

func (f *fakeDDB) UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.t.Fatal("eliminar un favorito no debe usar UpdateItem")
	return nil, nil
}

func testApp(ddb *fakeDDB) *functions.App {
	return &functions.App{
		Config: functions.Config{
			FavoritesTableName: testTableName,
			FunctionName:       "fn-eliminar-favorito-v1",
		},
		DDB:   ddb,
		Stage: "test",
	}
}

func testLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

func apiRequest(favoriteID string) events.APIGatewayV2HTTPRequest {
	return events.APIGatewayV2HTTPRequest{
		PathParameters: map[string]string{functions.FavoriteIDParam: favoriteID},
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			RequestID: testRequestID,
			Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
				Lambda: map[string]any{lambdautil.AuthorizerUserIDKey: testUserID},
			},
		},
	}
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

func TestHandleDeletesFavoriteFromAuthenticatedUserPartition(t *testing.T) {
	ddb := &fakeDDB{t: t}
	log, logs := testLogger()

	response, err := handle(context.Background(), testApp(ddb), log, apiRequest(testFavoriteID))
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if response.StatusCode != http.StatusNoContent || response.Body != "" {
		t.Errorf("respuesta = (%d, %q), want 204 sin cuerpo", response.StatusCode, response.Body)
	}
	if ddb.deleteInput == nil {
		t.Fatal("DeleteItem no fue invocado")
	}
	if got := aws.ToString(ddb.deleteInput.TableName); got != testTableName {
		t.Errorf("TableName = %q, want %q", got, testTableName)
	}
	if got := aws.ToString(ddb.deleteInput.ConditionExpression); got != "attribute_exists(pk) AND attribute_exists(sk)" {
		t.Errorf("ConditionExpression = %q", got)
	}

	var key domain.Key
	if err := attributevalue.UnmarshalMap(ddb.deleteInput.Key, &key); err != nil {
		t.Fatalf("no se pudo leer la clave: %v", err)
	}
	if key.PK != domain.UserPKPrefix+testUserID || key.SK != domain.ScopeProgram.SKPrefix()+testFavoriteID {
		t.Errorf("Key = %+v, want usuario del contexto e id del path", key)
	}

	entries := logs.FilterMessage("favoriteDeleted").All()
	if len(entries) != 1 {
		t.Fatalf("logs favoriteDeleted = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["favoriteId"] != testFavoriteID || fields["userId"] != testUserID || fields["operation"] != operationName {
		t.Errorf("campos del log = %+v", fields)
	}
}

func TestHandleReturnsNotFoundWhenFavoriteDoesNotExist(t *testing.T) {
	ddb := &fakeDDB{
		t: t,
		deleteErr: &types.ConditionalCheckFailedException{
			Message: aws.String("condition failed"),
		},
	}
	log, _ := testLogger()

	response, err := handle(context.Background(), testApp(ddb), log, apiRequest(testFavoriteID))
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if response.StatusCode != http.StatusNotFound || decodeError(t, response.Body) != apperr.CodeResourceNotFound {
		t.Errorf("respuesta = (%d, %s), want RESOURCE_NOT_FOUND", response.StatusCode, response.Body)
	}
}

func TestHandleRejectsInvalidIDAndMissingIdentityBeforeDeleting(t *testing.T) {
	tests := []struct {
		name       string
		request    events.APIGatewayV2HTTPRequest
		wantStatus int
		wantCode   string
	}{
		{
			name:       "id inválido",
			request:    apiRequest("no-es-ulid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   apperr.CodeValidationError,
		},
		{
			name: "identidad ausente",
			request: func() events.APIGatewayV2HTTPRequest {
				req := apiRequest(testFavoriteID)
				req.RequestContext.Authorizer = nil
				return req
			}(),
			wantStatus: http.StatusUnauthorized,
			wantCode:   apperr.CodeUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ddb := &fakeDDB{t: t}
			log, _ := testLogger()
			response, err := handle(context.Background(), testApp(ddb), log, tc.request)
			if err != nil {
				t.Fatalf("handle() error = %v", err)
			}
			if response.StatusCode != tc.wantStatus || decodeError(t, response.Body) != tc.wantCode {
				t.Errorf("respuesta = (%d, %s), want %s", response.StatusCode, response.Body, tc.wantCode)
			}
			if ddb.deleteInput != nil {
				t.Error("la solicitud inválida alcanzó DeleteItem")
			}
		})
	}
}

func TestHandleReturnsInternalErrorWhenDeleteFails(t *testing.T) {
	ddb := &fakeDDB{t: t, deleteErr: errors.New("dynamodb unavailable")}
	log, _ := testLogger()

	response, err := handle(context.Background(), testApp(ddb), log, apiRequest(testFavoriteID))
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if response.StatusCode != http.StatusInternalServerError || decodeError(t, response.Body) != apperr.CodeInternalError {
		t.Errorf("respuesta = (%d, %s), want INTERNAL_ERROR", response.StatusCode, response.Body)
	}
}
