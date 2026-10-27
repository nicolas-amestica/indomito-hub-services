package functions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

func requireAgentManager(request events.APIGatewayV2HTTPRequest) *apperr.AppError {
	identity, err := lambdautil.IdentityFromContext(request)
	if err != nil {
		return apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario")
	}
	if identity.ProfileCode != "ADM" {
		return apperr.New(apperr.CodeAuthForbidden, "El mantenedor requiere el perfil ADM")
	}
	return nil
}

func GetAgentManagement(ctx context.Context, service *domain.AgentManagementService, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if err := requireAgentManager(request); err != nil {
		return lambdautil.ErrorResponse(request, err)
	}
	result, err := service.Snapshot(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.Internal(err))
	}
	return lambdautil.SuccessResponse(http.StatusOK, result)
}

func SaveAgentDraft(ctx context.Context, service *domain.AgentManagementService, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if err := requireAgentManager(request); err != nil {
		return lambdautil.ErrorResponse(request, err)
	}
	var input domain.AgentManagedConfig
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be valid JSON"))
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must contain one object"))
	}
	result, err := service.SaveDraft(ctx, input)
	if err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("config", err.Error()))
	}
	return lambdautil.SuccessResponse(http.StatusOK, result)
}

func PublishAgentConfig(ctx context.Context, service *domain.AgentManagementService, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if err := requireAgentManager(request); err != nil {
		return lambdautil.ErrorResponse(request, err)
	}
	result, err := service.Publish(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("draft", err.Error()))
	}
	return lambdautil.SuccessResponse(http.StatusCreated, result)
}

func RestoreAgentConfig(ctx context.Context, service *domain.AgentManagementService, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if err := requireAgentManager(request); err != nil {
		return lambdautil.ErrorResponse(request, err)
	}
	var input struct {
		Version string `json:"version"`
	}
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Version) == "" {
		return lambdautil.ErrorResponse(request, invalidListQuery("version", "is required"))
	}
	result, err := service.Restore(ctx, input.Version)
	if err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("version", err.Error()))
	}
	return lambdautil.SuccessResponse(http.StatusCreated, result)
}

func PrepareAgentAsset(ctx context.Context, service *domain.AgentAssetUploadService, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if err := requireAgentManager(request); err != nil {
		return lambdautil.ErrorResponse(request, err)
	}
	var input struct {
		FileName string `json:"fileName"`
		Size     int64  `json:"size"`
	}
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be valid JSON"))
	}
	result, err := service.Prepare(ctx, input.FileName, input.Size)
	if err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("file", err.Error()))
	}
	return lambdautil.SuccessResponse(http.StatusOK, result)
}
