package lambdautil

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/labstack/echo/v4"

	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

// serveThroughAdapter registra handle en una ruta de Echo vía EchoAdapter y le
// entrega la solicitud indicada, devolviendo la respuesta HTTP resultante.
func serveThroughAdapter(
	t *testing.T,
	method, routePath string,
	req *http.Request,
	handle LambdaHandler,
) *httptest.ResponseRecorder {
	t.Helper()

	server := echo.New()
	server.Add(method, routePath, EchoAdapter(handle))

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)

	return recorder
}

// TestEchoAdapter_TranslatesRequest verifica que el puente traduzca método,
// path params, query params y cuerpo al evento que recibiría la función en AWS.
func TestEchoAdapter_TranslatesRequest(t *testing.T) {
	var captured events.APIGatewayV2HTTPRequest

	req := httptest.NewRequest(
		http.MethodPut,
		"/favoritos/01JQZ8FAV?includeTotals=true",
		strings.NewReader(`{"name":"gira"}`),
	)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)

	recorder := serveThroughAdapter(t, http.MethodPut, "/favoritos/:id", req,
		func(_ context.Context, event events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
			captured = event
			return SuccessResponse(http.StatusOK, map[string]string{"ok": "true"})
		})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get(echo.HeaderContentType); !strings.Contains(got, echo.MIMEApplicationJSON) {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	if captured.RequestContext.HTTP.Method != http.MethodPut {
		t.Errorf("method = %q, want %q", captured.RequestContext.HTTP.Method, http.MethodPut)
	}
	if captured.RouteKey != "PUT /favoritos/{id}" {
		t.Errorf("routeKey = %q, want PUT /favoritos/{id}", captured.RouteKey)
	}
	if captured.PathParameters["id"] != "01JQZ8FAV" {
		t.Errorf("pathParameters.id = %q, want 01JQZ8FAV", captured.PathParameters["id"])
	}
	if captured.QueryStringParameters["includeTotals"] != "true" {
		t.Errorf("queryStringParameters.includeTotals = %q, want true",
			captured.QueryStringParameters["includeTotals"])
	}
	if captured.Body != `{"name":"gira"}` {
		t.Errorf("body = %q, want el cuerpo original", captured.Body)
	}
	if captured.RequestContext.RequestID == "" {
		t.Error("requestId vacio: el traceId de una respuesta de error quedaria sin valor")
	}
	if captured.RequestContext.Stage != localStage {
		t.Errorf("stage = %q, want %q", captured.RequestContext.Stage, localStage)
	}
}

// TestEchoAdapter_InjectsDevUser verifica que el servidor local llene la
// identidad en el mismo lugar del contexto que usa el authorizer, para que el
// handler tenga un solo camino (Requirement 19.5, 19.8).
func TestEchoAdapter_InjectsDevUser(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/favoritos", nil)

	recorder := serveThroughAdapter(t, http.MethodGet, "/favoritos", req,
		func(_ context.Context, event events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
			userID, err := UserIDFromContext(event)
			if err != nil {
				return ErrorResponse(event, err)
			}
			return SuccessResponse(http.StatusOK, map[string]string{"userId": userID})
		})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: el usuario de desarrollo no llego al contexto", recorder.Code, http.StatusOK)
	}

	var decoded struct {
		Data struct {
			UserID string `json:"userId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("no se pudo decodificar el cuerpo: %v", err)
	}
	if decoded.Data.UserID != DevUserID {
		t.Errorf("userId = %q, want %q", decoded.Data.UserID, DevUserID)
	}
}

// TestEchoAdapter_DevUserIsOverridable verifica que se pueda correr el servidor
// local con otro usuario para probar el aislamiento entre particiones.
func TestEchoAdapter_DevUserIsOverridable(t *testing.T) {
	t.Setenv(DevUserIDEnv, "01JQZ8OTROUSUARIO")

	req := httptest.NewRequest(http.MethodGet, "/favoritos", nil)

	var captured string
	serveThroughAdapter(t, http.MethodGet, "/favoritos", req,
		func(_ context.Context, event events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
			captured, _ = UserIDFromContext(event)
			return SuccessResponse(http.StatusOK, nil)
		})

	if captured != "01JQZ8OTROUSUARIO" {
		t.Errorf("userId = %q, want 01JQZ8OTROUSUARIO", captured)
	}
}

// TestEchoAdapter_WritesErrorEnvelope verifica que la respuesta de error del
// handler llegue al cliente con su estado y su envelope, en vez de traducirse a
// un error de Echo.
func TestEchoAdapter_WritesErrorEnvelope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/favoritos/01JQZ8FAV", nil)

	recorder := serveThroughAdapter(t, http.MethodGet, "/favoritos/:id", req,
		func(_ context.Context, event events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
			return ErrorResponse(event, apperr.NotFound("El favorito no existe"))
		})

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}

	decoded := decodeErrorBody(t, recorder.Body.String())
	if decoded.Code != apperr.CodeResourceNotFound {
		t.Errorf("code = %q, want %q", decoded.Code, apperr.CodeResourceNotFound)
	}
	if decoded.TraceID == "" {
		t.Error("traceId vacio en la respuesta de error")
	}
}

// TestEchoAdapter_NormalizesReturnedError verifica que un handler que devuelve
// el error como segundo valor, en vez de dentro de la respuesta, no deje al
// cliente sin envelope.
func TestEchoAdapter_NormalizesReturnedError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/favoritos", nil)

	recorder := serveThroughAdapter(t, http.MethodGet, "/favoritos", req,
		func(_ context.Context, _ events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
			return events.APIGatewayV2HTTPResponse{}, errors.New("falla inesperada")
		})

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}

	decoded := decodeErrorBody(t, recorder.Body.String())
	if decoded.Code != apperr.CodeInternalError {
		t.Errorf("code = %q, want %q", decoded.Code, apperr.CodeInternalError)
	}
}
