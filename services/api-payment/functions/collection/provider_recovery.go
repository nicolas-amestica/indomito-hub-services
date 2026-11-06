package collection

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

const providerReviewProbeInterval = time.Hour

// recoverProviderFailure diferencia errores externos de fallos de persistencia.
// Solo un error conocido de Khipu puede reprogramarse como consulta fallida.
func (a AutomaticReconciliationApp) recoverProviderFailure(ctx context.Context, job record, cause error, now time.Time) error {
	var failure *providers.KhipuError
	if !errors.As(cause, &failure) {
		if !errors.Is(cause, providers.ErrVerification) {
			return cause
		}
		failure = &providers.KhipuError{Code: "VERIFICATION_FAILED"}
	}
	// Un ingreso o resolución ya registrados no deben depender de que el
	// proveedor siga disponible para cerrar su trabajo de recuperación.
	if status, err := a.localTerminalStatus(ctx, job); err != nil {
		return err
	} else if status != "" {
		return a.Accounts.finishReconciliation(ctx, job, record{PK: "ATTEMPT#" + job.AttemptID, SK: "PROVIDER_STATUS", AccountID: job.AccountID, AttemptID: job.AttemptID, Status: status, CheckedAt: now.Unix()})
	}
	status := "RECONCILIATION_REQUIRED"
	retryAt := now.Add(automaticReconciliationDelay(job.Count + 1))
	if !failure.Retryable {
		status = "PROVIDER_REVIEW_REQUIRED"
		retryAt = now.Add(providerReviewProbeInterval)
	}
	if failure.RetryAt.After(retryAt) {
		retryAt = failure.RetryAt
	}
	job.ProviderFailures++
	job.ProviderRetryAt = retryAt.Unix()
	if retryAt.Nanosecond() > 0 {
		job.ProviderRetryAt++
	}
	retryAt = time.Unix(job.ProviderRetryAt, 0)
	observation := record{PK: "ATTEMPT#" + job.AttemptID, SK: "PROVIDER_STATUS", AccountID: job.AccountID, AttemptID: job.AttemptID, Status: status, ProviderDetail: failure.Code, CheckedAt: now.Unix(), ProviderRetryAt: job.ProviderRetryAt}
	next := providerNextWake(now, retryAt)
	if err := a.Accounts.rescheduleReconciliation(ctx, job, observation, next); err != nil {
		return err
	}
	logProviderFailure(a.Log, a.Stage, job.AttemptID, failure, !failure.Retryable || job.ProviderFailures >= 3, now)
	return nil
}

// waitForProviderRetry conserva el plazo real aunque SQS limite su demora a
// quince minutos. Una entrega anticipada no ejecuta llamadas al proveedor.
func (a AutomaticReconciliationApp) waitForProviderRetry(ctx context.Context, job record, now time.Time) error {
	if status, err := a.localTerminalStatus(ctx, job); err != nil {
		return err
	} else if status != "" {
		return a.Accounts.finishReconciliation(ctx, job, record{PK: "ATTEMPT#" + job.AttemptID, SK: "PROVIDER_STATUS", AccountID: job.AccountID, AttemptID: job.AttemptID, Status: status, CheckedAt: now.Unix()})
	}
	observation, err := a.Accounts.read(ctx, "ATTEMPT#"+job.AttemptID, "PROVIDER_STATUS")
	if err != nil {
		return err
	}
	if observation.AccountID != job.AccountID || observation.AttemptID != job.AttemptID {
		return ErrReplayMismatch
	}
	next := providerNextWake(now, time.Unix(job.ProviderRetryAt, 0))
	return a.Accounts.writeRescheduledReconciliation(ctx, job, nil, next)
}

func providerNextWake(now, retryAt time.Time) time.Time {
	next := now.Add(15 * time.Minute)
	if retryAt.Before(next) {
		return retryAt
	}
	return next
}

func (a AutomaticReconciliationApp) localTerminalStatus(ctx context.Context, job record) (string, error) {
	for _, sk := range []string{"RESOLUTION", "OUTCOME"} {
		if _, err := a.Accounts.read(ctx, "ATTEMPT#"+job.AttemptID, sk); errors.Is(err, ErrNotFound) {
			continue
		} else if err != nil {
			return "", err
		}
		account, err := a.Accounts.GetAccount(ctx, job.AccountID)
		if err != nil {
			return "", err
		}
		view, err := (PortalApp{Accounts: a.Accounts, Now: a.Now}).attemptView(ctx, account, job.AttemptID)
		if err != nil {
			return "", err
		}
		switch view.Status {
		case "CONFIRMED", "REVIEW_REQUIRED", "UNPAID_FINAL", "REVERSED":
			return view.Status, nil
		default:
			return "", ErrReplayMismatch
		}
	}
	return "", nil
}

func logProviderFailure(log *zap.Logger, stage, attemptID string, failure *providers.KhipuError, alert bool, now time.Time) {
	if log == nil {
		return
	}
	fields := []zap.Field{zap.String("attemptId", attemptID), zap.String("failureCode", failure.Code), zap.Int("httpStatus", failure.HTTPStatus), zap.Bool("retryableQuery", failure.Retryable), zap.String("Stage", stage)}
	if alert {
		fields = append(fields, zap.Int("ProviderRecoveryRequired", 1), zap.Any("_aws", map[string]any{"Timestamp": now.UnixMilli(), "CloudWatchMetrics": []any{map[string]any{"Namespace": "Indomito/Payments", "Dimensions": [][]string{{"Stage"}}, "Metrics": []any{map[string]string{"Name": "ProviderRecoveryRequired", "Unit": "Count"}}}}}))
	}
	log.Warn("Consulta o creacion Khipu fallida; conservar bloqueo y revisar evidencia", fields...)
}
