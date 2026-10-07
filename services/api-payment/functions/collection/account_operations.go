package collection

import (
	"context"
	"net/mail"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// AccountOperationsApp expone comandos administrativos sin credenciales bancarias.
type AccountOperationsApp struct {
	Accounts Service
	Now      func() time.Time
}

// AccountOperationRequest exige versión revisada, motivo y clave durable por operación.
type AccountOperationRequest struct {
	CommandID      string   `json:"commandId"`
	Version        int64    `json:"version"`
	Operation      string   `json:"operation"`
	Reason         string   `json:"reason"`
	Amount         int64    `json:"amount"`
	BasisPoints    int64    `json:"basisPoints"`
	InstallmentIDs []string `json:"installmentIds"`
	Reference      string   `json:"reference"`
	EffectiveDate  string   `json:"effectiveDate"`
	Email          string   `json:"email"`
}

// Handle permite leer una cuenta o registrar movimientos confirmados por personal autorizado.
// No ejecuta transferencias ni aprueba bajas: estas deben provenir del anexo contractual.
func (a AccountOperationsApp) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		return adminFailure(req, 401, "UNAUTHORIZED")
	}
	if req.RequestContext.Authorizer.Lambda["paymentAccess"] != "admin" {
		return adminFailure(req, 403, "FORBIDDEN")
	}
	id := req.PathParameters["id"]
	if !validPortalID(id) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if req.RequestContext.HTTP.Method == "GET" {
		account, err := a.Accounts.GetAccount(ctx, id)
		if err != nil {
			return domainFailure(req, err)
		}
		return lambdautil.SuccessResponseWithHeaders(200, account, map[string]string{"cache-control": "no-store"})
	}
	if req.RequestContext.HTTP.Method != "POST" || a.Now == nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	var body AccountOperationRequest
	if decodeAdmin(req, &body) != nil || !validPortalID(body.CommandID) || body.Version < 1 || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	commandID := paymentOperationID("admin:" + id + ":" + body.CommandID)
	audit := domain.Audit{CommandID: commandID, Actor: actor, Reason: strings.TrimSpace(body.Reason), RecordedAt: a.Now().UTC()}
	command := Command{ID: commandID, AccountID: id, ExpectedVersion: body.Version, Payload: body}
	var transition func(domain.Account) (domain.Change, error)
	switch body.Operation {
	case "ALLOCATE_UNAPPLIED":
		transition = func(current domain.Account) (domain.Change, error) {
			return domain.AllocateUnappliedFunds(current, audit, body.InstallmentIDs)
		}
	case "DISCOUNT":
		transition = func(current domain.Account) (domain.Change, error) {
			return domain.Discount(current, audit, body.InstallmentIDs, body.BasisPoints)
		}
	case "APPROVE_WITHDRAWAL_REFUND":
		transition = func(current domain.Account) (domain.Change, error) {
			return domain.ApproveWithdrawalRefund(current, audit, body.BasisPoints)
		}
	case "APPROVE_UNAPPLIED_REFUND":
		transition = func(current domain.Account) (domain.Change, error) {
			return domain.ApproveUnappliedRefund(current, audit, body.Amount)
		}
	case "RESOLVE_REFUNDED_REVIEW":
		transition = func(current domain.Account) (domain.Change, error) {
			return domain.ResolveRefundedReview(current, audit)
		}
	case "MANUAL_INSTALLMENT", "RECORD_DEPOSIT", "CONFIRM_REFUND":
		date, dateErr := time.Parse(time.DateOnly, body.EffectiveDate)
		// Se admite la fecha local de Chile; no se inventan movimientos futuros.
		zone, zoneErr := time.LoadLocation("America/Santiago")
		if zoneErr != nil {
			return domainFailure(req, zoneErr)
		}
		if dateErr != nil || date.Format(time.DateOnly) > a.Now().In(zone).Format(time.DateOnly) || len(body.Reference) < 5 || len(body.Reference) > 150 || strings.TrimSpace(body.Reference) != body.Reference {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		command.Reference = "bank:" + body.Reference
		if body.Operation != "CONFIRM_REFUND" {
			address, emailErr := mail.ParseAddress(body.Email)
			if emailErr != nil || address.Address != body.Email || len(body.Email) > 254 {
				return adminFailure(req, 400, "INVALID_REQUEST")
			}
			command.ReceiptID = commandID
			command.ReceiptEmail = body.Email
		}
		switch body.Operation {
		case "MANUAL_INSTALLMENT":
			if body.Amount != 0 {
				return adminFailure(req, 400, "INVALID_REQUEST")
			}
			transition = func(current domain.Account) (domain.Change, error) {
				return domain.RecordManualInstallment(current, audit, command.Reference, body.EffectiveDate)
			}
		case "RECORD_DEPOSIT":
			transition = func(current domain.Account) (domain.Change, error) {
				return domain.RecordDeposit(current, audit, body.Amount, command.Reference, body.EffectiveDate)
			}
		case "CONFIRM_REFUND":
			transition = func(current domain.Account) (domain.Change, error) {
				return domain.ConfirmRefund(current, audit, body.Amount, command.Reference, body.EffectiveDate)
			}
		}
	default:
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	change, err := a.Accounts.Apply(ctx, command, transition)
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, change, map[string]string{"cache-control": "no-store"})
}
