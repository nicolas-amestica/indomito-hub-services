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

const (
	authorizerEmailKey       = "email"
	authorizerRUTKey         = "rut"
	authorizerProfileCodeKey = "profileCode"
)

// AuthorizerIdentity contiene la identidad validada que publicó el Lambda
// Authorizer. Los campos opcionales son cadenas vacías cuando no existen.
type AuthorizerIdentity struct {
	UserID      string
	Email       string
	RUT         string
	ProfileCode string
}

// IdentityFromContext valida y normaliza el contexto completo del authorizer.
// Una clave presente con tipo, longitud o contenido inválido invalida toda la
// identidad; nunca se intenta rescatarla desde una fuente controlada por el cliente.
func IdentityFromContext(req events.APIGatewayV2HTTPRequest) (AuthorizerIdentity, error) {
	authorizer := req.RequestContext.Authorizer
	if authorizer == nil || len(authorizer.Lambda) == 0 {
		return AuthorizerIdentity{}, unauthorizedIdentity()
	}

	userID, err := requiredIdentityValue(authorizer.Lambda, AuthorizerUserIDKey, authorizerSubjectKey, 128)
	if err != nil {
		return AuthorizerIdentity{}, err
	}
	email, err := optionalIdentityValue(authorizer.Lambda, authorizerEmailKey, 254, strings.ToLower)
	if err != nil {
		return AuthorizerIdentity{}, err
	}
	rut, err := optionalIdentityValue(authorizer.Lambda, authorizerRUTKey, 32, func(value string) string { return value })
	if err != nil {
		return AuthorizerIdentity{}, err
	}
	profileCode, err := optionalIdentityValue(authorizer.Lambda, authorizerProfileCodeKey, 64, strings.ToUpper)
	if err != nil {
		return AuthorizerIdentity{}, err
	}

	return AuthorizerIdentity{UserID: userID, Email: email, RUT: rut, ProfileCode: profileCode}, nil
}

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
	identity, err := IdentityFromContext(req)
	return identity.UserID, err
}

func requiredIdentityValue(values map[string]any, primary string, fallback string, maximum int) (string, error) {
	if _, present := values[primary]; present {
		return identityString(values[primary], maximum)
	}
	value, present := values[fallback]
	if !present {
		return "", unauthorizedIdentity()
	}
	return identityString(value, maximum)
}

func optionalIdentityValue(values map[string]any, key string, maximum int, normalize func(string) string) (string, error) {
	value, present := values[key]
	if !present {
		return "", nil
	}
	text, err := identityString(value, maximum)
	if err != nil {
		return "", err
	}
	return normalize(text), nil
}

func identityString(value any, maximum int) (string, error) {
	text, valid := value.(string)
	if !valid {
		return "", unauthorizedIdentity()
	}
	text = strings.TrimSpace(text)
	if text == "" || len(text) > maximum {
		return "", unauthorizedIdentity()
	}
	return text, nil
}

func unauthorizedIdentity() error {
	return apperr.Unauthorized("No se pudo identificar al usuario de la solicitud")
}
