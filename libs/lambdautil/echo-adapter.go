package lambdautil

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/labstack/echo/v4"
	"github.com/oklog/ulid/v2"

	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/resp"
)

// LambdaHandler es la firma de la función Handle que expone cada
// functions/<endpoint>/endpoint.go.
type LambdaHandler func(
	ctx context.Context,
	req events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error)

// DevUserID es el identificador del usuario de desarrollo que [EchoAdapter]
// inyecta en `requestContext.authorizer.lambda`, el mismo lugar donde el
// Lambda Authorizer lo publica en AWS. Gracias a eso el handler tiene un solo
// camino para obtener la identidad ([UserIDFromContext]) y no necesita saber si
// corre local o desplegado.
//
// Es un ULID fijo a propósito: los favoritos que se crean en local quedan
// todos en la misma partición y se pueden listar entre sesiones.
const DevUserID = "01JDEV0000000000000000DEV0"

// DevUserIDEnv es la variable de entorno que permite correr el servidor local
// con otro usuario, para probar el aislamiento entre particiones sin recompilar.
const DevUserIDEnv = "LOCAL_DEV_USER_ID"

// localStage es el stage que reporta el contexto de una solicitud servida por
// el servidor local. No coincide con ningún ambiente desplegado (dev/prd), así
// que un log de local nunca se confunde con uno de AWS.
const localStage = "local"

// EchoAdapter adapta un [LambdaHandler] a un handler de Echo, traduciendo la
// solicitud HTTP a un evento APIGatewayV2HTTPRequest y la respuesta del evento
// de vuelta a HTTP. Es lo que permite ejercitar los endpoints de punta a punta
// con `make dev service=services/api-<nombre>` mientras el authorizer
// compartido no esté activo (Requirement 19.5), sin escribir un segundo
// handler para local.
//
// El evento que construye trae el usuario de desarrollo en el contexto del
// authorizer: ver [DevUserID].
func EchoAdapter(handle LambdaHandler) echo.HandlerFunc {
	return func(echoCtx echo.Context) error {
		req, err := requestFromEcho(echoCtx)
		if err != nil {
			return writeResponse(echoCtx, resp.Error(req.RequestContext.RequestID, apperr.From(err)))
		}

		response, err := handle(echoCtx.Request().Context(), req)
		if err != nil {
			// Un handler bien escrito devuelve el error dentro de la
			// respuesta, no como segundo valor. Si igual lo devuelve, acá se
			// normaliza en vez de dejar que Echo responda un 500 sin envelope.
			response = resp.Error(req.RequestContext.RequestID, apperr.From(err))
		}

		return writeResponse(echoCtx, response)
	}
}

// requestFromEcho traduce la solicitud HTTP de Echo al evento que recibiría la
// función en AWS.
func requestFromEcho(echoCtx echo.Context) (events.APIGatewayV2HTTPRequest, error) {
	httpReq := echoCtx.Request()
	requestID := ulid.Make().String()
	now := time.Now().UTC()

	req := events.APIGatewayV2HTTPRequest{
		Version:               "2.0",
		RouteKey:              routeKey(httpReq.Method, echoCtx.Path()),
		RawPath:               httpReq.URL.Path,
		RawQueryString:        httpReq.URL.RawQuery,
		Headers:               collapseHeaders(httpReq.Header),
		QueryStringParameters: collapseQueryParams(echoCtx.QueryParams()),
		PathParameters:        pathParameters(echoCtx),
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			RouteKey:  routeKey(httpReq.Method, echoCtx.Path()),
			Stage:     localStage,
			RequestID: requestID,
			Time:      now.Format("02/Jan/2006:15:04:05 -0700"),
			TimeEpoch: now.UnixMilli(),
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
				Method:    httpReq.Method,
				Path:      httpReq.URL.Path,
				Protocol:  httpReq.Proto,
				SourceIP:  echoCtx.RealIP(),
				UserAgent: httpReq.UserAgent(),
			},
			Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
				Lambda: map[string]any{
					AuthorizerUserIDKey: devUserID(),
				},
			},
		},
	}

	if httpReq.Body != nil {
		body, err := io.ReadAll(httpReq.Body)
		if err != nil {
			return req, apperr.InvalidRequestFormat("No se pudo leer el cuerpo de la solicitud").
				WithCause(err)
		}
		req.Body = string(body)
	}

	return req, nil
}

// writeResponse escribe la respuesta del evento en la respuesta HTTP de Echo,
// resolviendo el cuerpo en base64 que devuelven los endpoints binarios como el
// PDF del presupuesto.
func writeResponse(echoCtx echo.Context, response events.APIGatewayV2HTTPResponse) error {
	for key, value := range response.Headers {
		echoCtx.Response().Header().Set(key, value)
	}
	for key, values := range response.MultiValueHeaders {
		for _, value := range values {
			echoCtx.Response().Header().Add(key, value)
		}
	}

	body := []byte(response.Body)
	if response.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(response.Body)
		if err != nil {
			return err
		}
		body = decoded
	}

	contentType := echoCtx.Response().Header().Get(echo.HeaderContentType)
	if contentType == "" {
		contentType = echo.MIMEApplicationJSON
	}

	statusCode := response.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	return echoCtx.Blob(statusCode, contentType, body)
}

// devUserID devuelve el usuario de desarrollo, con [DevUserIDEnv] por delante
// de [DevUserID] cuando está definida.
func devUserID() string {
	if fromEnv := strings.TrimSpace(os.Getenv(DevUserIDEnv)); fromEnv != "" {
		return fromEnv
	}
	return DevUserID
}

// routeKey arma la routeKey del evento con la sintaxis de API Gateway
// (`GET /favoritos/{id}`) a partir de la de Echo (`/favoritos/:id`), para que
// un handler que la lea vea lo mismo en local y en AWS.
func routeKey(method, echoPath string) string {
	segments := strings.Split(echoPath, "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			segments[index] = "{" + segment[1:] + "}"
		}
	}
	return method + " " + strings.Join(segments, "/")
}

// pathParameters traduce los parámetros de ruta de Echo al mapa del evento.
// Devuelve nil cuando la ruta no tiene parámetros, igual que API Gateway, que
// omite el campo.
func pathParameters(echoCtx echo.Context) map[string]string {
	names := echoCtx.ParamNames()
	if len(names) == 0 {
		return nil
	}

	values := echoCtx.ParamValues()
	params := make(map[string]string, len(names))
	for index, name := range names {
		if index < len(values) {
			params[name] = values[index]
		}
	}
	return params
}

// collapseHeaders aplana las cabeceras al mapa de valor único del evento,
// uniendo las repetidas con coma y pasando el nombre a minúsculas, que es como
// API Gateway v2 las entrega.
func collapseHeaders(source http.Header) map[string]string {
	return collapseValues(source, strings.ToLower)
}

// collapseQueryParams aplana los query params al mapa de valor único del
// evento. A diferencia de las cabeceras, el nombre conserva su capitalización:
// los query params del proyecto son camelCase.
func collapseQueryParams(source map[string][]string) map[string]string {
	return collapseValues(source, func(key string) string { return key })
}

// collapseValues aplana un mapa de múltiples valores al mapa de valor único
// del evento, uniendo los repetidos con coma como lo hace API Gateway v2.
func collapseValues(source map[string][]string, normalizeKey func(string) string) map[string]string {
	if len(source) == 0 {
		return nil
	}

	collapsed := make(map[string]string, len(source))
	for key, values := range source {
		collapsed[normalizeKey(key)] = strings.Join(values, ",")
	}
	return collapsed
}
