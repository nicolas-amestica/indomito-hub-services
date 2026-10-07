package collection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/paymentaccess"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// AdminApp no comparte sesiones ni credenciales del portal público.
type AdminApp struct {
	LookupSecret string
	Accounts     Service
	Contracts    ContractSource
}

// SetupRequest permite únicamente elegir liberados y confirmar la revisión contractual.
type SetupRequest struct {
	ContractVersion    int64    `json:"contractVersion"`
	FreeParticipantIDs []string `json:"freeParticipantIds"`
}

// HandleSetup obtiene la instantánea o ejecuta una puesta en marcha autorizada e idempotente.
func (a AdminApp) HandleSetup(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		return adminFailure(req, 401, "UNAUTHORIZED")
	}
	if req.RequestContext.Authorizer.Lambda["paymentAccess"] != "admin" {
		return adminFailure(req, 403, "FORBIDDEN")
	}
	method := req.RequestContext.HTTP.Method
	if method != "GET" && method != "POST" {
		return adminFailure(req, 400, "METHOD_NOT_ALLOWED")
	}
	view, err := a.Contracts.LoadApproved(ctx, req.PathParameters["id"])
	if err != nil {
		return domainFailure(req, err)
	}
	if method == "GET" {
		view.Status = "NOT_STARTED"
		view.FreeParticipantIDs = []string{}
		plan, planErr := a.Accounts.read(ctx, "TRIP#"+view.ContractID, "META")
		if planErr == nil {
			snapshot, snapshotErr := a.Accounts.read(ctx, plan.PK, "SETUP")
			if snapshotErr != nil {
				return domainFailure(req, snapshotErr)
			}
			if snapshot.Startup == nil || snapshot.Fingerprint != plan.Fingerprint || snapshot.Startup.ContractVersion != view.ContractVersion {
				return domainFailure(req, collection.ErrConflict)
			}
			view.Status = plan.Status
			for _, person := range snapshot.Startup.Participants {
				if person.Free {
					view.FreeParticipantIDs = append(view.FreeParticipantIDs, person.ID)
				}
			}
		} else if !errors.Is(planErr, ErrNotFound) {
			return domainFailure(req, planErr)
		}
		return lambdautil.SuccessResponseWithHeaders(200, view, map[string]string{"cache-control": "no-store"})
	}
	var body SetupRequest
	if err = decodeAdmin(req, &body); err != nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	startup, err := view.Startup(body.ContractVersion, body.FreeParticipantIDs)
	if err != nil {
		return domainFailure(req, err)
	}
	if len(a.LookupSecret) < 32 {
		return domainFailure(req, errors.New("configuración de búsqueda incompleta"))
	}
	startup.LookupKeys = map[string]string{}
	for _, member := range view.Members {
		lookup, lookupErr := paymentaccess.RUTKey(a.LookupSecret, member.DNI)
		if lookupErr == nil {
			startup.LookupKeys[member.ID] = lookup
		} // Documentos extranjeros se gestionan por administración, no como RUT.
	}
	if err = a.Accounts.PreparePlan(ctx, startup, actor); err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, map[string]any{"tripId": view.ContractID, "status": "ACTIVE", "participantCount": len(view.Members), "freeCount": view.FreeCount}, map[string]string{"cache-control": "no-store"})
}

func decodeAdmin(req events.APIGatewayV2HTTPRequest, target any) error {
	if len(req.Body) > 64*1024 {
		return collection.ErrInvalid
	}
	body := req.Body
	if req.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return collection.ErrInvalid
		}
		body = string(decoded)
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return collection.ErrInvalid
	}
	return nil
}

func domainFailure(req events.APIGatewayV2HTTPRequest, err error) (events.APIGatewayV2HTTPResponse, error) {
	switch {
	case errors.Is(err, ErrNotFound):
		return adminFailure(req, 404, "PAYMENT_ACCOUNT_NOT_FOUND")
	case errors.Is(err, ErrReplayMismatch), errors.Is(err, ErrReferenceUsed), errors.Is(err, collection.ErrConflict):
		return adminFailure(req, 409, "PAYMENT_STATE_CONFLICT")
	case errors.Is(err, collection.ErrInvalid):
		return adminFailure(req, 400, "INVALID_PAYMENT_CONFIGURATION")
	default:
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
}

func adminFailure(req events.APIGatewayV2HTTPRequest, status int, reason string) (events.APIGatewayV2HTTPResponse, error) {
	code := map[int]string{400: apperr.CodeValidationError, 401: apperr.CodeUnauthorized, 403: apperr.CodeForbidden, 404: apperr.CodeResourceNotFound, 409: apperr.CodeConflict, 502: apperr.CodeUpstreamServiceError}[status]
	if code == "" {
		code = apperr.CodeInternalError
	}
	message := "No fue posible realizar la operación de pagos."
	if reason == "INVALID_PAYMENT_CONFIGURATION" {
		message = "Revise los liberados, las condiciones y las fechas de vencimiento del contrato."
	}
	response, err := lambdautil.ErrorResponse(req, apperr.New(code, message))
	if response.Headers == nil {
		response.Headers = map[string]string{}
	}
	response.Headers["cache-control"] = "no-store"
	return response, err
}
