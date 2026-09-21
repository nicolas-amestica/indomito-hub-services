// Package lambdautil reúne lo que todo handler Lambda del backend repite:
// leer el cuerpo JSON de la solicitud, validarlo, construir la respuesta con
// el envelope del contrato, extraer la identidad del usuario del contexto del
// authorizer y exponer ese mismo handler vía Echo para el servidor local.
//
// El paquete no contiene lógica de negocio ni conoce el dominio: es el borde
// entre API Gateway y las funciones de cada endpoint. El flujo de un
// endpoint, según docs/standards/backend/go-conventions.md, es
// [BindJSON] → [ValidateStruct] → lógica de negocio →
// [SuccessResponse] o [ErrorResponse].
package lambdautil

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

// BindJSON decodifica el cuerpo JSON de la solicitud en target, que debe ser
// un puntero a struct. Resuelve el cuerpo en base64 que API Gateway entrega
// cuando isBase64Encoded viene en true, para que el handler no tenga que
// distinguir los dos casos.
//
// Devuelve un *apperr.AppError con código INVALID_REQUEST_FORMAT cuando el
// cuerpo está ausente o el JSON está mal formado: en ambos casos la solicitud
// es inutilizable y no alcanza a validarse. La validación de los campos ya
// decodificados es responsabilidad de [ValidateStruct], que distingue entre un
// campo obligatorio ausente y un valor fuera de rango.
func BindJSON(req events.APIGatewayV2HTTPRequest, target any) error {
	body := req.Body

	if req.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return apperr.InvalidRequestFormat("El cuerpo de la solicitud no está codificado correctamente").
				WithCause(err)
		}
		body = string(decoded)
	}

	if strings.TrimSpace(body) == "" {
		return apperr.InvalidRequestFormat("El cuerpo de la solicitud está vacío")
	}

	if err := json.Unmarshal([]byte(body), target); err != nil {
		return apperr.InvalidRequestFormat("El cuerpo de la solicitud no es un JSON válido").
			WithCause(err)
	}

	return nil
}
