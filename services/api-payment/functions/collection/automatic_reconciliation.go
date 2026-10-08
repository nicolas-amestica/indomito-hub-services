package collection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

const reconciliationReviewAfter = 24 * time.Hour

type reconciliationMessage struct {
	AttemptID string `json:"attemptId"`
	AccountID string `json:"accountId"`
	JobSK     string `json:"jobSk"`
	Count     int64  `json:"count"`
}

// AutomaticReconciliationApp recupera confirmaciones cuando el webhook no llega.
type AutomaticReconciliationApp struct {
	Accounts  Service
	Inspector PaymentInspector
	Verifier  PaymentVerifier
	Zone      *time.Location
	Now       func() time.Time
}

func automaticReconciliationDelay(attempt int64) time.Duration {
	switch {
	case attempt < 1:
		return time.Minute
	case attempt < 2:
		return 2 * time.Minute
	case attempt < 3:
		return 5 * time.Minute
	default:
		return 15 * time.Minute
	}
}

func (a AutomaticReconciliationApp) process(ctx context.Context, message reconciliationMessage) error {
	job, err := a.Accounts.read(ctx, reconciliationPendingPK, message.JobSK)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if job.AttemptID != message.AttemptID || job.AccountID != message.AccountID || job.Count != message.Count || job.Status != "PENDING" {
		return ErrReplayMismatch
	}
	now := a.Now().UTC()
	audit := domain.Audit{CommandID: paymentOperationID(fmt.Sprintf("automatic-reconciliation:%s:%d", job.AttemptID, job.Count)), Actor: "system:khipu-reconciliation", Reason: "Conciliacion automatica de intento pendiente", RecordedAt: now}
	state, err := a.Accounts.ReconcileProviderAttempt(ctx, a.Inspector, a.Verifier, job.AttemptID, "", audit, now, a.Zone)
	if err != nil {
		return err
	}
	portalStatus := state.Status
	if state.Status == "PENDING" {
		portalStatus = "PENDING_PAYMENT"
		if state.ProviderStatus == "verifying" {
			portalStatus = "VERIFYING_PROVIDER"
		}
	}
	checkout, checkoutErr := a.Accounts.read(ctx, "ATTEMPT#"+job.AttemptID, "CHECKOUT")
	if checkoutErr != nil || checkout.Checkout == nil {
		return checkoutErr
	}
	if state.Status == "PENDING" && now.After(checkout.Checkout.ExpiresAt.Add(reconciliationReviewAfter)) {
		portalStatus, state.Status = "PROVIDER_REVIEW_REQUIRED", "PROVIDER_REVIEW_REQUIRED"
	}
	observation := record{PK: "ATTEMPT#" + job.AttemptID, SK: "PROVIDER_STATUS", AttemptID: job.AttemptID, AccountID: job.AccountID, Status: portalStatus, ProviderStatus: state.ProviderStatus, ProviderDetail: state.ProviderDetail, CheckedAt: now.Unix()}
	if state.Status != "PENDING" {
		return a.Accounts.finishReconciliation(ctx, job, observation)
	}
	next := now.Add(automaticReconciliationDelay(job.Count + 1))
	return a.Accounts.rescheduleReconciliation(ctx, job, observation, next)
}

func decodeReconciliationMessage(body string) (reconciliationMessage, error) {
	var message reconciliationMessage
	err := json.Unmarshal([]byte(body), &message)
	if err != nil || !validPortalID(message.AttemptID) || message.AccountID == "" || message.JobSK == "" || message.Count < 0 {
		return reconciliationMessage{}, domain.ErrInvalid
	}
	return message, nil
}

func (s Service) finishReconciliation(ctx context.Context, job, observation record) error {
	observationWrite, err := s.put(observation, "attribute_not_exists(pk) OR checkedAt < :checked", map[string]types.AttributeValue{":checked": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", observation.CheckedAt)}})
	if err != nil {
		return err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{reconciliationDelete(s.Table, job), observationWrite}})
	return err
}

func (s Service) rescheduleReconciliation(ctx context.Context, job, observation record, next time.Time) error {
	observationWrite, err := s.put(observation, "attribute_not_exists(pk) OR checkedAt < :checked", map[string]types.AttributeValue{":checked": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", observation.CheckedAt)}})
	if err != nil {
		return err
	}
	nextJob := job
	nextJob.SK = reconciliationJobSK(next, job.AttemptID)
	nextJob.Count++
	nextJob.NextAttemptAt = next.Unix()
	nextWrite, err := s.put(nextJob, "attribute_not_exists(pk)", nil)
	if err != nil {
		return err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{reconciliationDelete(s.Table, job), nextWrite, observationWrite}})
	return err
}

func reconciliationDelete(table string, job record) types.TransactWriteItem {
	return types.TransactWriteItem{Delete: &types.Delete{TableName: &table, Key: key(job.PK, job.SK), ConditionExpression: aws.String("#status = :pending"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":pending": &types.AttributeValueMemberS{Value: "PENDING"}}}}
}
