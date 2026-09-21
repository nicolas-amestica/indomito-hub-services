// Package resp construye las respuestas HTTP de API Gateway v2 con el
// envelope de exito y de error del contrato descrito en
// docs/standards/global/api-design.md y docs/standards/global/error-handling.md:
//
//	{ "data": ... }
//
//	{ "code": "...", "message": "...", "details": {...}, "traceId": "..." }
//
// El traceId de una respuesta de error siempre proviene del requestId del
// evento de API Gateway, nunca se genera en este paquete: es lo que permite
// cruzar la respuesta con los access logs del stage (ver observability.md).
package resp

import (
	"encoding/json"

	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

const contentTypeJSON = "application/json"

// successEnvelope es el cuerpo de toda respuesta exitosa.
type successEnvelope struct {
	Data any `json:"data"`
}

// errorEnvelope es el cuerpo de toda respuesta de error, segun el contrato
// de api-design.md.
type errorEnvelope struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
	TraceID string         `json:"traceId"`
}

// Success construye la respuesta HTTP exitosa con el envelope { data } y el
// codigo de estado indicado. requestID no participa de una respuesta
// exitosa (el contrato de exito no lleva traceId), pero se recibe para
// mantener la misma firma que [Error] en los handlers.
func Success(statusCode int, data any) events.APIGatewayV2HTTPResponse {
	body, err := json.Marshal(successEnvelope{Data: data})
	if err != nil {
		return Error(requestIDUnavailable, apperr.Internal(err))
	}
	return events.APIGatewayV2HTTPResponse{
		StatusCode: statusCode,
		Headers:    jsonHeaders(),
		Body:       string(body),
	}
}

// requestIDUnavailable se usa unicamente cuando Success falla al serializar
// su propio cuerpo: un caso extremo en el que de todas formas hay que
// devolver un error al cliente. No es un traceId real.
const requestIDUnavailable = ""

// Error construye la respuesta HTTP de error a partir de un *apperr.AppError,
// con el envelope { code, message, details, traceId } y el estado HTTP que
// el propio AppError declara. requestID es el requestId del evento de API
// Gateway (events.APIGatewayV2HTTPRequest.RequestContext.RequestID) y se usa
// como traceId de la respuesta.
func Error(requestID string, err *apperr.AppError) events.APIGatewayV2HTTPResponse {
	if err == nil {
		err = apperr.Internal(nil)
	}
	body, marshalErr := json.Marshal(errorEnvelope{
		Code:    err.Code(),
		Message: err.Message(),
		Details: err.Details(),
		TraceID: requestID,
	})
	if marshalErr != nil {
		// Fallback sin dependencias: un error al serializar el propio error
		// no debe dejar la invocacion sin respuesta.
		body = []byte(`{"code":"INTERNAL_ERROR","message":"Ocurrio un error inesperado","traceId":"` + requestID + `"}`)
	}
	return events.APIGatewayV2HTTPResponse{
		StatusCode: err.HTTPStatus(),
		Headers:    jsonHeaders(),
		Body:       string(body),
	}
}

func jsonHeaders() map[string]string {
	return map[string]string{
		"Content-Type": contentTypeJSON,
	}
}
