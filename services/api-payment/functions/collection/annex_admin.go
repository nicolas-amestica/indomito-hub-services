package collection

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/paymentaccess"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// AnnexAdminApp mantiene identidad administrativa y tiempo fuera del JSON confiado.
type AnnexAdminApp struct {
	Accounts     Service
	LookupSecret string
	Now          func() time.Time
}

type AnnexAdmissionRequest struct {
	AccountID     string                     `json:"accountId"`
	ParticipantID string                     `json:"participantId"`
	DNI           string                     `json:"dni"`
	Name          string                     `json:"name"`
	Free          bool                       `json:"free"`
	DepositAgreed int64                      `json:"depositAgreed"`
	Installments  []domain.AgreedInstallment `json:"installments"`
}

type AnnexDraftRequest struct {
	ID           string                  `json:"id"`
	Reason       string                  `json:"reason"`
	Withdrawals  []AnnexWithdrawal       `json:"withdrawals"`
	Admissions   []AnnexAdmissionRequest `json:"admissions"`
	Replacements []AnnexReplacement      `json:"replacements,omitempty"`
}

type AnnexApprovalRequest struct {
	CommandID string `json:"commandId"`
	Reason    string `json:"reason"`
}

func annexAdminIdentity(req events.APIGatewayV2HTTPRequest) (string, *events.APIGatewayV2HTTPResponse) {
	actor, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		response, _ := adminFailure(req, 401, "UNAUTHORIZED")
		return "", &response
	}
	if req.RequestContext.Authorizer.Lambda["paymentAccess"] != "admin" {
		response, _ := adminFailure(req, 403, "FORBIDDEN")
		return "", &response
	}
	return actor, nil
}

// HandleCreateAnnex prepara un borrador; no lo aprueba ni bloquea la cobranza.
func (a AnnexAdminApp) HandleCreateAnnex(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	tripID := req.PathParameters["tripId"]
	if req.RequestContext.HTTP.Method != "POST" || !validPortalID(tripID) || a.Now == nil || len(a.LookupSecret) < 32 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	var body AnnexDraftRequest
	if decodeAdmin(req, &body) != nil || !validPortalID(body.ID) || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	reason := strings.TrimSpace(body.Reason)
	recordedAt := a.Now().UTC()
	if root, readErr := a.Accounts.read(ctx, "TRIP#"+tripID, "ANNEX#"+body.ID); readErr == nil && root.Event != nil && root.Event.CommandID == body.ID && root.Event.Actor == actor && root.Event.Reason == reason {
		recordedAt = root.Event.RecordedAt
	}
	input := AnnexDraftInput{ID: body.ID, TripID: tripID, Withdrawals: body.Withdrawals, Replacements: body.Replacements, LookupKeys: map[string]string{}, Profiles: map[string]AnnexIdentity{}, Audit: domain.Audit{CommandID: body.ID, Actor: actor, Reason: reason, RecordedAt: recordedAt}}
	for _, admission := range body.Admissions {
		if strings.TrimSpace(admission.Name) == "" || strings.TrimSpace(admission.DNI) == "" {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		input.Admissions = append(input.Admissions, domain.Admission{AccountID: admission.AccountID, ParticipantID: admission.ParticipantID, TripID: tripID, AnnexID: body.ID, Free: admission.Free, DepositAgreed: admission.DepositAgreed, Installments: admission.Installments})
		input.Profiles[admission.AccountID] = AnnexIdentity{Name: strings.TrimSpace(admission.Name), Document: strings.TrimSpace(admission.DNI)}
		if strings.TrimSpace(admission.DNI) != "" {
			lookup, err := paymentaccess.RUTKey(a.LookupSecret, admission.DNI)
			if err != nil {
				return adminFailure(req, 400, "INVALID_REQUEST")
			}
			input.LookupKeys[admission.AccountID] = lookup
		}
	}
	state, err := a.Accounts.PrepareAnnexDraft(ctx, input)
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, state, map[string]string{"cache-control": "no-store"})
}

func (a AnnexAdminApp) HandleGetAnnex(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	state, err := a.Accounts.GetAnnexDraft(ctx, req.PathParameters["tripId"], req.PathParameters["annexId"])
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, state, map[string]string{"cache-control": "no-store"})
}

func (a AnnexAdminApp) HandleRoster(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	page, err := a.Accounts.ListRoster(ctx, req.PathParameters["tripId"], req.QueryStringParameters["cursor"])
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, page, map[string]string{"cache-control": "no-store"})
}

func (a AnnexAdminApp) HandlePreviewAnnex(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	page, err := a.Accounts.PreviewAnnexDraft(ctx, req.PathParameters["tripId"], req.PathParameters["annexId"], req.QueryStringParameters["cursor"])
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, page, map[string]string{"cache-control": "no-store"})
}

func (a AnnexAdminApp) HandleAnnexImpact(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	impact, err := a.Accounts.SummarizeAnnexDraft(ctx, req.PathParameters["tripId"], req.PathParameters["annexId"])
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, impact, map[string]string{"cache-control": "no-store"})
}

func (a AnnexAdminApp) HandleApplyAnnex(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	if req.RequestContext.HTTP.Method != "POST" || a.Now == nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	var body AnnexApprovalRequest
	if decodeAdmin(req, &body) != nil || !validPortalID(body.CommandID) || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	tripID, annexID := req.PathParameters["tripId"], req.PathParameters["annexId"]
	commandID := paymentOperationID("annex-approval:" + annexID + ":" + body.CommandID)
	recordedAt := a.Now().UTC()
	// El servidor fija el tiempo la primera vez. Un retry con la misma clave reutiliza
	// el instante persistido para que una respuesta perdida no cambie la huella.
	if root, readErr := a.Accounts.read(ctx, "TRIP#"+tripID, "ANNEX#"+annexID); readErr == nil && root.Approval != nil && root.Approval.CommandID == commandID {
		recordedAt = root.Approval.RecordedAt
	}
	approval := domain.Audit{CommandID: commandID, Actor: actor, Reason: strings.TrimSpace(body.Reason), RecordedAt: recordedAt}
	if err := a.Accounts.ApplyAnnexWithApproval(ctx, tripID, annexID, approval); err != nil {
		return domainFailure(req, err)
	}
	state, err := a.Accounts.GetAnnexDraft(ctx, tripID, annexID)
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, state, map[string]string{"cache-control": "no-store"})
}
