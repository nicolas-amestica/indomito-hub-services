// Package apperr define errores de dominio que se traducen a respuestas
// HTTP. Cada [AppError] lleva un codigo del catalogo de
// docs/standards/global/api-design.md (seccion "Codigos de respuestas
// personalizadas"), el estado HTTP fijo que le corresponde a ese codigo y un
// mensaje en espanol pensado para llegar al usuario final.
//
// Los constructores de este archivo son la unica forma soportada de crear un
// AppError. No agregar codigos nuevos: el catalogo de api-design.md es la
// fuente de verdad y esta libreria no lo extiende.
//
// Convencion de nivel de log: un AppError con estado HTTP menor a 500 es un
// error esperado (validacion, no encontrado, conflicto, etc.) y se registra
// en warn, nunca en error. Un AppError con estado 500 o superior, o un error
// que no es un AppError, es inesperado y se registra en error. Ver
// [AppError.IsExpected] y libs/logger.WarnOrError, que aplica esta regla.
package apperr

import (
	"fmt"
)

// Códigos del catálogo de docs/standards/global/api-design.md. No agregar
// códigos nuevos sin actualizar ese documento primero.
const (
	// CodeValidationError es el error generico de validacion (400).
	CodeValidationError = "VALIDATION_ERROR"
	// CodeRequiredFieldMissing marca un campo obligatorio ausente (400).
	CodeRequiredFieldMissing = "REQUIRED_FIELD_MISSING"
	// CodeInvalidQueryParameter marca un query param invalido (400).
	CodeInvalidQueryParameter = "INVALID_QUERY_PARAMETER"
	// CodeInvalidRequestFormat marca un cuerpo con JSON mal formado (400).
	CodeInvalidRequestFormat = "INVALID_REQUEST_FORMAT"
	// CodeUnauthorized marca un token invalido o ausente (401).
	CodeUnauthorized = "UNAUTHORIZED"
	// CodeTokenExpired marca un token vencido (401).
	CodeTokenExpired = "TOKEN_EXPIRED"
	// CodeForbidden marca la ausencia de permisos (403).
	CodeForbidden = "FORBIDDEN"
	// CodeResourceNotFound marca un recurso que no existe (404).
	CodeResourceNotFound = "RESOURCE_NOT_FOUND"
	// CodeConflict marca un duplicado o un estado invalido (409).
	CodeConflict = "CONFLICT"
	// CodeUpstreamServiceError marca la falla de un servicio externo (502).
	CodeUpstreamServiceError = "UPSTREAM_SERVICE_ERROR"
	// CodeUpstreamTimeout marca una dependencia externa que no responde (502).
	CodeUpstreamTimeout = "UPSTREAM_TIMEOUT"
	// CodeInternalError marca un error inesperado (500).
	CodeInternalError = "INTERNAL_ERROR"
)

// httpStatusByCode mapea cada codigo del catalogo a su estado HTTP fijo,
// segun la tabla "Codigos de respuestas personalizadas" de api-design.md.
var httpStatusByCode = map[string]int{
	CodeValidationError:       400,
	CodeRequiredFieldMissing:  400,
	CodeInvalidQueryParameter: 400,
	CodeInvalidRequestFormat:  400,
	CodeUnauthorized:          401,
	CodeTokenExpired:          401,
	CodeForbidden:             403,
	CodeResourceNotFound:      404,
	CodeConflict:              409,
	CodeUpstreamServiceError:  502,
	CodeUpstreamTimeout:       502,
	CodeInternalError:         500,
}

// AppError es un error de dominio con su codigo del catalogo, su estado HTTP
// y un mensaje en espanol apto para el usuario final. Implementa error.
type AppError struct {
	code       string
	httpStatus int
	message    string
	details    map[string]any
	cause      error
}

// Error devuelve una representacion apta para logs, no para la respuesta
// HTTP: incluye el codigo y, si existe, la causa envuelta. La respuesta HTTP
// la construye libs/shared/resp a partir de Code, Message y Details.
func (e *AppError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.code, e.message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.code, e.message)
}

// Unwrap expone la causa original para errors.Is / errors.As.
func (e *AppError) Unwrap() error {
	return e.cause
}

// Code devuelve el codigo del catalogo de api-design.md.
func (e *AppError) Code() string {
	return e.code
}

// HTTPStatus devuelve el estado HTTP con el que debe responder el endpoint.
func (e *AppError) HTTPStatus() int {
	return e.httpStatus
}

// Message devuelve el mensaje en espanol apto para el usuario final.
func (e *AppError) Message() string {
	return e.message
}

// Details devuelve los datos adicionales del error, o nil si no tiene. Solo
// debe contener datos que el cliente ya envio: nunca detalles internos como
// nombres de tabla, queries o mensajes crudos del SDK.
func (e *AppError) Details() map[string]any {
	return e.details
}

// IsExpected indica si el error corresponde a una condicion esperada (4xx) y
// por lo tanto debe registrarse en warn, no en error. Ver libs/logger.WarnOrError.
func (e *AppError) IsExpected() bool {
	return e.httpStatus < 500
}

// WithDetails agrega datos adicionales a un AppError existente y lo devuelve
// para encadenar. Solo debe usarse con datos que el cliente ya envio.
func (e *AppError) WithDetails(details map[string]any) *AppError {
	e.details = details
	return e
}

// WithCause adjunta la causa original del error, para que quede disponible
// via Unwrap en el log estructurado. La causa nunca se expone en la
// respuesta HTTP.
func (e *AppError) WithCause(cause error) *AppError {
	e.cause = cause
	return e
}

// New construye un AppError a partir de un codigo del catalogo de
// api-design.md. Si el codigo no esta en el catalogo, hace panic: es un
// error de programacion (un codigo mal escrito o inventado), no una
// condicion de runtime que un handler deba manejar.
func New(code string, message string) *AppError {
	status, ok := httpStatusByCode[code]
	if !ok {
		panic(fmt.Sprintf("apperr: codigo desconocido %q, no esta en el catalogo de api-design.md", code))
	}
	return &AppError{code: code, httpStatus: status, message: message}
}

// Validation construye un VALIDATION_ERROR (400).
func Validation(message string) *AppError {
	return New(CodeValidationError, message)
}

// Validationf construye un VALIDATION_ERROR (400) con mensaje formateado.
func Validationf(format string, args ...any) *AppError {
	return New(CodeValidationError, fmt.Sprintf(format, args...))
}

// RequiredFieldMissing construye un REQUIRED_FIELD_MISSING (400).
func RequiredFieldMissing(message string) *AppError {
	return New(CodeRequiredFieldMissing, message)
}

// RequiredFieldMissingf construye un REQUIRED_FIELD_MISSING (400) con
// mensaje formateado.
func RequiredFieldMissingf(format string, args ...any) *AppError {
	return New(CodeRequiredFieldMissing, fmt.Sprintf(format, args...))
}

// InvalidQueryParameter construye un INVALID_QUERY_PARAMETER (400).
func InvalidQueryParameter(message string) *AppError {
	return New(CodeInvalidQueryParameter, message)
}

// InvalidRequestFormat construye un INVALID_REQUEST_FORMAT (400).
func InvalidRequestFormat(message string) *AppError {
	return New(CodeInvalidRequestFormat, message)
}

// Unauthorized construye un UNAUTHORIZED (401).
func Unauthorized(message string) *AppError {
	return New(CodeUnauthorized, message)
}

// TokenExpired construye un TOKEN_EXPIRED (401).
func TokenExpired(message string) *AppError {
	return New(CodeTokenExpired, message)
}

// Forbidden construye un FORBIDDEN (403).
func Forbidden(message string) *AppError {
	return New(CodeForbidden, message)
}

// NotFound construye un RESOURCE_NOT_FOUND (404).
func NotFound(message string) *AppError {
	return New(CodeResourceNotFound, message)
}

// NotFoundf construye un RESOURCE_NOT_FOUND (404) con mensaje formateado.
func NotFoundf(format string, args ...any) *AppError {
	return New(CodeResourceNotFound, fmt.Sprintf(format, args...))
}

// Conflict construye un CONFLICT (409).
func Conflict(message string) *AppError {
	return New(CodeConflict, message)
}

// UpstreamServiceError construye un UPSTREAM_SERVICE_ERROR (502).
func UpstreamServiceError(message string) *AppError {
	return New(CodeUpstreamServiceError, message)
}

// UpstreamTimeout construye un UPSTREAM_TIMEOUT (502).
func UpstreamTimeout(message string) *AppError {
	return New(CodeUpstreamTimeout, message)
}

// Internal construye un INTERNAL_ERROR (500) a partir de una falla
// inesperada. El mensaje que llega al cliente es generico a proposito: la
// causa real (cause) solo se expone en el log estructurado, nunca en la
// respuesta HTTP.
func Internal(cause error) *AppError {
	err := New(CodeInternalError, "Ocurrio un error inesperado")
	return err.WithCause(cause)
}

// From normaliza cualquier error a un AppError: si err ya es (o envuelve) un
// AppError lo devuelve sin cambios, y en caso contrario lo envuelve como
// [Internal]. Es el punto que usa un endpoint antes de pasarle el error a
// libs/shared/resp.Error, para no tener que distinguir el tipo en cada
// handler.
func From(err error) *AppError {
	if err == nil {
		return nil
	}
	if appErr, ok := asAppError(err); ok {
		return appErr
	}
	return Internal(err)
}

// asAppError recorre la cadena de Unwrap buscando un *AppError, replicando
// errors.As sin importar el paquete errors solo para este caso.
func asAppError(err error) (*AppError, bool) {
	for err != nil {
		if appErr, ok := err.(*AppError); ok {
			return appErr, true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return nil, false
		}
		err = unwrapper.Unwrap()
	}
	return nil, false
}
