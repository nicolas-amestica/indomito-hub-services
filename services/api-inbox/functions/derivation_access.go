package functions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

// DerivationAccess permite únicamente canjear y leer permisos de derivación.
type DerivationAccess interface {
	Redeem(context.Context, string) (domain.DerivationAccessSession, error)
	Read(context.Context, string, string) (domain.DerivationAccessPage, error)
}

var accessOnce sync.Once
var accessInstance *domain.DerivationAccessService
var accessError error

// GetDerivationAccess carga solo DynamoDB y configuración SecureString de esta capacidad.
func GetDerivationAccess(ctx context.Context) (*domain.DerivationAccessService, error) {
	accessOnce.Do(func() {
		cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
		if err != nil {
			accessError = err
			return
		}
		var config domain.DerivationNotificationConfig
		err = readSecureJSON(ctx, ssm.NewFromConfig(cfg), os.Getenv("INBOX_DERIVATION_NOTIFICATION_CONFIG_PARAM"), &config)
		if err != nil {
			accessError = err
			return
		}
		accessInstance, accessError = domain.NewDerivationAccessService(dynamodb.NewFromConfig(cfg), os.Getenv("CONVERSATIONS_TABLE_NAME"), config.HubBaseURL)
	})
	return accessInstance, accessError
}

var derivationContextIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func derivationSessionFromContext(request events.APIGatewayV2HTTPRequest) (string, error) {
	identity, err := lambdautil.IdentityFromContext(request)
	if err != nil || request.RequestContext.Authorizer == nil {
		return "", apperr.New(apperr.CodeAuthUnauthorized, "Se requiere acceso temporal")
	}
	values := request.RequestContext.Authorizer.Lambda
	sessionID, valid := values["derivationSessionId"].(string)
	scope, _ := values["derivationScope"].(string)
	if !valid || !derivationContextIDPattern.MatchString(sessionID) || scope != "conversation:read" || identity.UserID != "derivation:"+sessionID {
		return "", apperr.New(apperr.CodeAuthUnauthorized, "Se requiere acceso temporal")
	}
	return sessionID, nil
}

// DerivationAccessRequest canjea el enlace público; la lectura exige contexto del authorizer.
func DerivationAccessRequest(ctx context.Context, service DerivationAccess, request events.APIGatewayV2HTTPRequest, redeem bool) (events.APIGatewayV2HTTPResponse, error) {
	headers := map[string]string{"cache-control": "no-store, private", "referrer-policy": "no-referrer", "x-content-type-options": "nosniff"}
	var authorizedSession string
	if !redeem {
		var contextErr error
		authorizedSession, contextErr = derivationSessionFromContext(request)
		if contextErr != nil {
			return derivationAccessError(request, contextErr, headers)
		}
	}
	if len(request.Body) > 2048 || request.IsBase64Encoded {
		return derivationAccessError(request, apperr.New(apperr.CodeValidationError, "Solicitud inválida"), headers)
	}
	var body struct {
		Token        *string `json:"token"`
		SessionToken *string `json:"sessionToken"`
		Before       *string `json:"before"`
	}
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return derivationAccessError(request, apperr.New(apperr.CodeValidationError, "Solicitud inválida"), headers)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return derivationAccessError(request, apperr.New(apperr.CodeValidationError, "Solicitud inválida"), headers)
	}
	var result any
	var err error
	if redeem {
		if body.Token == nil || body.SessionToken != nil || body.Before != nil {
			return derivationAccessError(request, apperr.New(apperr.CodeValidationError, "Solicitud inválida"), headers)
		}
		result, err = service.Redeem(ctx, *body.Token)
	} else {
		if body.Token != nil || body.SessionToken != nil || body.Before == nil {
			return derivationAccessError(request, apperr.New(apperr.CodeValidationError, "Solicitud inválida"), headers)
		}
		result, err = service.Read(ctx, authorizedSession, *body.Before)
	}
	if err != nil {
		return derivationAccessError(request, err, headers)
	}
	return lambdautil.SuccessResponseWithHeaders(http.StatusOK, result, headers)
}
func derivationAccessError(request events.APIGatewayV2HTTPRequest, err error, headers map[string]string) (events.APIGatewayV2HTTPResponse, error) {
	expired := errors.Is(err, domain.ErrDerivationAccessInvalid)
	if expired {
		err = apperr.New(apperr.CodeResourceNotFound, "El acceso temporal ya no está disponible")
	}
	response, _ := lambdautil.ErrorResponse(request, err)
	if expired {
		response.StatusCode = http.StatusGone
	}
	if response.Headers == nil {
		response.Headers = map[string]string{}
	}
	for key, value := range headers {
		response.Headers[key] = value
	}
	return response, nil
}
