package lambdautil

import (
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

// AuthorizerUserIDKey es la clave bajo la cual el Lambda Authorizer
// compartido publica el identificador del usuario dentro de
// `requestContext.authorizer.lambda`. Es contrato con el repo
// ind-hub-iam-tsx-sls-pri-gh: cambiarla acá sin cambiarla allá deja a todo
// endpoint protegido sin poder identificar a quien llama.
const AuthorizerUserIDKey = "userId"

// authorizerSubjectKey es la clave alternativa que se acepta cuando el
// contexto viene de un token con claims estándar, donde el sujeto es `sub`.
const authorizerSubjectKey = "sub"

// UserIDFromContext extrae el identificador del usuario del contexto del
// authorizer. Devuelve error si el contexto no lo trae: un endpoint protegido
// que no puede identificar a quien llama no debe continuar.
//
// El identificador se lee siempre de `requestContext.authorizer.lambda`, nunca
// del cuerpo ni de una cabecera (Requirement 19.8). Un `userId` que llegue en
// el cuerpo se ignora, porque el cliente puede escribir lo que quiera ahí y el
// contexto del authorizer no.
//
// En desarrollo local quien llena ese lugar del contexto es [EchoAdapter], con
// el usuario fijo [DevUserID]. El handler tiene un solo camino: lee del
// contexto y no sabe quién lo llenó.
func UserIDFromContext(req events.APIGatewayV2HTTPRequest) (string, error) {
	authorizer := req.RequestContext.Authorizer
	if authorizer == nil || len(authorizer.Lambda) == 0 {
		return "", apperr.Unauthorized("No se pudo identificar al usuario de la solicitud")
	}

	for _, key := range []string{AuthorizerUserIDKey, authorizerSubjectKey} {
		value, present := authorizer.Lambda[key]
		if !present {
			continue
		}
		// El contexto de un Lambda Authorizer entrega valores de texto: un
		// tipo distinto es un contexto mal armado, no una identidad válida.
		text, isString := value.(string)
		if !isString {
			continue
		}
		if userID := strings.TrimSpace(text); userID != "" {
			return userID, nil
		}
	}

	return "", apperr.Unauthorized("No se pudo identificar al usuario de la solicitud")
}
