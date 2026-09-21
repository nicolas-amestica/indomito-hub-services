package lambdautil

import (
	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/resp"
)

// SuccessResponse construye la respuesta exitosa con el envelope { data } y el
// estado HTTP indicado.
//
// Devuelve dos valores para que un handler pueda cerrar con
// `return lambdautil.SuccessResponse(...)`. El segundo siempre es nil: una
// función Lambda integrada con API Gateway debe devolver la respuesta, no el
// error, porque un error devuelto se traduce en un 502 sin cuerpo y el cliente
// pierde el code y el traceId del contrato.
func SuccessResponse(statusCode int, data any) (events.APIGatewayV2HTTPResponse, error) {
	return resp.Success(statusCode, data), nil
}

// SuccessResponseWithHeaders es [SuccessResponse] más cabeceras propias del
// endpoint, como el `Cache-Control` de los catálogos. Las cabeceras recibidas
// se agregan sobre las del envelope y pueden sobrescribirlas.
func SuccessResponseWithHeaders(
	statusCode int,
	data any,
	headers map[string]string,
) (events.APIGatewayV2HTTPResponse, error) {
	response := resp.Success(statusCode, data)

	if response.Headers == nil {
		response.Headers = make(map[string]string, len(headers))
	}
	for key, value := range headers {
		response.Headers[key] = value
	}

	return response, nil
}

// ErrorResponse construye la respuesta de error con el envelope
// { code, message, details, traceId } a partir de cualquier error: un
// *apperr.AppError viaja con su código y su estado, y cualquier otro error se
// normaliza a INTERNAL_ERROR sin exponer su mensaje crudo al cliente.
//
// Recibe la solicitud porque el traceId de la respuesta es el requestId del
// evento de API Gateway, que es lo que permite cruzarla con los access logs
// del stage. Es la única razón por la que la firma no es ErrorResponse(err).
func ErrorResponse(
	req events.APIGatewayV2HTTPRequest,
	err error,
) (events.APIGatewayV2HTTPResponse, error) {
	return resp.Error(req.RequestContext.RequestID, apperr.From(err)), nil
}
