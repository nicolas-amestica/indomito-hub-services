package collection

import (
	"context"
	"errors"
	"strings"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

type PaymentInspector interface {
	InspectPayment(context.Context, string, string) (providers.VerifiedPayment, error)
}
type AttemptReconciliationState struct {
	AttemptID      string `json:"attemptId"`
	PaymentID      string `json:"paymentId"`
	Status         string `json:"status"`
	ProviderStatus string `json:"providerStatus"`
	ProviderDetail string `json:"providerDetail"`
	Amount         int64  `json:"amount"`
}

func (s Service) ReconcileProviderAttempt(ctx context.Context, inspector PaymentInspector, verifier PaymentVerifier, attemptID, suppliedPaymentID string, audit domain.Audit, now time.Time, zone *time.Location) (AttemptReconciliationState, error) {
	if inspector == nil || verifier == nil || attemptID == "" || audit.CommandID == "" || audit.Actor == "" || audit.Reason == "" || audit.RecordedAt.IsZero() || now.IsZero() || zone == nil {
		return AttemptReconciliationState{}, domain.ErrInvalid
	}
	row, err := s.read(ctx, "ATTEMPT#"+attemptID, "META")
	if err != nil || row.Attempt == nil {
		return AttemptReconciliationState{}, err
	}
	attempt := *row.Attempt
	if resolution, resolutionErr := s.read(ctx, "ATTEMPT#"+attemptID, "RESOLUTION"); resolutionErr == nil && resolution.Event != nil && resolution.AccountID == attempt.AccountID {
		status := "UNPAID_FINAL"
		paymentID := strings.TrimPrefix(resolution.Event.Reference, "khipu-unpaid:")
		if resolution.Event.Type == "PAYMENT_REVERSED_BY_PROVIDER" {
			status = "REVERSED"
			paymentID = strings.TrimPrefix(resolution.Event.Reference, "khipu-reversal:")
		}
		return AttemptReconciliationState{AttemptID: attemptID, PaymentID: paymentID, Status: status, Amount: resolution.Event.Amount}, nil
	}
	paymentID := suppliedPaymentID
	checkout, checkoutErr := s.read(ctx, "ATTEMPT#"+attemptID, "CHECKOUT")
	if checkoutErr == nil {
		if checkout.Checkout == nil {
			return AttemptReconciliationState{}, domain.ErrInvalid
		}
		if paymentID != "" && paymentID != checkout.Checkout.PaymentID {
			return AttemptReconciliationState{}, ErrReplayMismatch
		}
		paymentID = checkout.Checkout.PaymentID
	} else if !errors.Is(checkoutErr, ErrNotFound) {
		return AttemptReconciliationState{}, checkoutErr
	}
	if !checkoutPaymentID.MatchString(paymentID) {
		return AttemptReconciliationState{}, domain.ErrInvalid
	}
	verified, err := inspector.InspectPayment(ctx, paymentID, attemptID)
	if err != nil {
		return AttemptReconciliationState{}, err
	}
	amount, err := providers.ConfirmedCLPAmount(verified.Amount)
	if err != nil {
		return AttemptReconciliationState{}, providers.ErrVerification
	}
	state := AttemptReconciliationState{AttemptID: attemptID, PaymentID: paymentID, Status: "PENDING", ProviderStatus: verified.Status, ProviderDetail: verified.StatusDetail, Amount: amount}
	if verified.Status == "done" && verified.StatusDetail == "normal" {
		change, e := s.ConfirmProviderCheckout(ctx, verifier, attemptID, paymentID, now, zone)
		if e != nil {
			return AttemptReconciliationState{}, e
		}
		if change.Event.Type == "PAYMENT_REQUIRES_REVIEW" {
			state.Status = "REVIEW_REQUIRED"
		} else {
			state.Status = "CONFIRMED"
		}
		return state, nil
	}
	if verified.Status == "done" && verified.StatusDetail == "reversed" {
		outcome, e := s.read(ctx, "ATTEMPT#"+attemptID, "OUTCOME")
		if e != nil || outcome.Event == nil {
			return AttemptReconciliationState{}, domain.ErrConflict
		}
		account, e := s.GetAccount(ctx, attempt.AccountID)
		if e != nil {
			return AttemptReconciliationState{}, e
		}
		settlement, settlementErr := s.read(ctx, "TRIP#"+account.TripID, "SETTLEMENT#khipu:"+paymentID)
		if settlementErr != nil || settlement.Settlement == nil {
			return AttemptReconciliationState{}, domain.ErrConflict
		}
		// Una reversa posterior a la liquidacion ya movio dinero bancario. No se
		// revierte automaticamente como cuenta por cobrar al proveedor: tesoreria
		// debe registrar la salida real y su evidencia para no falsear la caja.
		if settlement.Settlement.SettledGross > 0 {
			state.Status = "REVIEW_REQUIRED"
			return state, nil
		}
		commandID := paymentOperationID("khipu-reversal:" + paymentID)
		reference := "khipu-reversal:" + paymentID
		command := Command{ID: commandID, AccountID: account.ID, ExpectedVersion: account.Version, Reference: reference, Payload: verified, ResolvedAttemptID: attemptID}
		change, e := s.Apply(ctx, command, func(current domain.Account) (domain.Change, error) {
			return domain.ReverseProviderPayment(current, domain.Audit{CommandID: commandID, Actor: audit.Actor, Reason: audit.Reason, RecordedAt: audit.RecordedAt}, attempt, outcome.Event.Type, amount, reference, now.In(zone).Format(time.DateOnly))
		})
		if e != nil {
			return AttemptReconciliationState{}, e
		}
		_ = change
		state.Status = "REVERSED"
		return state, nil
	}
	finalDetail := verified.StatusDetail == "pending" || verified.StatusDetail == "rejected-by-payer" || verified.StatusDetail == "marked-as-abuse"
	if verified.Status == "pending" && finalDetail && !verified.ExpiresDate.IsZero() && !verified.ExpiresDate.After(now) {
		account, e := s.GetAccount(ctx, attempt.AccountID)
		if e != nil {
			return AttemptReconciliationState{}, e
		}
		commandID := paymentOperationID("khipu-unpaid:" + paymentID)
		reference := "khipu-unpaid:" + paymentID
		attempt.Status = "UNPAID_FINAL"
		command := Command{ID: commandID, AccountID: account.ID, ExpectedVersion: account.Version, Reference: reference, Payload: verified, ResolvedAttemptID: attemptID}
		_, e = s.Apply(ctx, command, func(current domain.Account) (domain.Change, error) {
			return domain.ResolveUnpaid(current, domain.Audit{CommandID: commandID, Actor: audit.Actor, Reason: audit.Reason, RecordedAt: audit.RecordedAt}, attempt, reference)
		})
		if e != nil {
			return AttemptReconciliationState{}, e
		}
		state.Status = "UNPAID_FINAL"
		return state, nil
	}
	if verified.StatusDetail == "marked-paid-by-receiver" {
		state.Status = "REVIEW_REQUIRED"
	}
	return state, nil
}
