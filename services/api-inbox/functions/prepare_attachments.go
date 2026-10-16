package functions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"unicode"
)

type AttachmentPreparer interface {
	Prepare(context.Context, string, string, string, []domain.AttachmentPreparationItem) (domain.PrepareAttachmentsResponse, error)
}
type prepareAttachmentsRequest struct {
	BatchID string                             `json:"batchId"`
	Files   []domain.AttachmentPreparationItem `json:"files"`
}

var outgoingMIMEs = map[string]bool{"application/pdf": true, "image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "audio/ogg": true, "audio/webm": true}

func PrepareAttachments(ctx context.Context, service AttachmentPreparer, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	identity, err := lambdautil.IdentityFromContext(request)
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeAuthUnauthorized, "No se pudo identificar al usuario"))
	}
	phone := request.PathParameters["telefono"]
	if !phonePattern.MatchString(phone) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeValidationPhoneInvalid, "El telefono no es valido"))
	}
	var input prepareAttachmentsRequest
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must be valid JSON"))
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return lambdautil.ErrorResponse(request, invalidListQuery("body", "must contain one JSON object"))
	}
	if !uuidPattern.MatchString(input.BatchID) || len(input.Files) < 1 || len(input.Files) > 20 {
		return lambdautil.ErrorResponse(request, invalidListQuery("files", "batchId must be a UUID and files must contain 1 to 20 items"))
	}
	seen := map[string]bool{}
	for index, file := range input.Files {
		digest, decodeErr := base64.StdEncoding.DecodeString(file.ChecksumSHA256)
		canonical := decodeErr == nil && len(digest) == 32 && base64.StdEncoding.EncodeToString(digest) == file.ChecksumSHA256
		if !uuidPattern.MatchString(file.ClientMessageID) || seen[file.ClientMessageID] || len([]rune(file.FileName)) < 1 || len([]rune(file.FileName)) > 200 || filepath.Base(file.FileName) != file.FileName || strings.IndexFunc(file.FileName, unicode.IsControl) >= 0 || !outgoingMIMEs[file.MIMEType] || file.Size < 1 || file.Size > 7*1024*1024 || !canonical {
			return lambdautil.ErrorResponse(request, invalidListQuery("files", fmt.Sprintf("item %d is invalid", index)))
		}
		seen[file.ClientMessageID] = true
	}
	result, err := service.Prepare(ctx, identity.UserID, phone, input.BatchID, input.Files)
	if errors.Is(err, domain.ErrUploadConflict) {
		return lambdautil.ErrorResponse(request, apperr.New(apperr.CodeInboxIdempotencyConflict, "El lote o uno de sus identificadores ya existe"))
	}
	if err != nil {
		return lambdautil.ErrorResponse(request, apperr.Internal(err))
	}
	return lambdautil.SuccessResponse(http.StatusOK, result)
}
