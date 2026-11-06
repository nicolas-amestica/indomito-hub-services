package collection

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

const (
	reconciliationRecoveryPK    = "RECONCILIATION#RECOVERY"
	reconciliationRecoveryLimit = 20
	reconciliationRecoveryGrace = 5 * time.Minute
	reconciliationRecoveryLease = 15 * time.Minute
	reconciliationSweepLease    = 2 * time.Minute
)

const recoveryJobCondition = "#status = :pending AND nextAttemptAt <= :cutoff AND (attribute_not_exists(recoveryLeaseUntil) OR recoveryLeaseUntil <= :now) AND (attribute_not_exists(providerRetryAt) OR providerRetryAt <= :now)"

// ReconciliationRecoveryApp recupera trabajos vencidos sin consultar Khipu ni
// tocar cuentas financieras. Una ejecución hace como máximo una Query de 20 filas.
type ReconciliationRecoveryApp struct {
	Accounts Service
	Queue    reconciliationQueue
	QueueURL string
	Now      func() time.Time
}

// Run recorre la partición pendiente con cursor persistente y concesiones CAS.
// Un SendMessage ambiguo conserva el trabajo; SQS y el consumidor son idempotentes.
func (a ReconciliationRecoveryApp) Run(ctx context.Context) error {
	if a.Now == nil || a.Queue == nil || a.QueueURL == "" || a.Accounts.Table == "" || a.Accounts.DB == nil {
		return errors.New("configuracion de recuperacion incompleta")
	}
	db, ok := a.Accounts.DB.(queryDatabase)
	if !ok {
		return errors.New("query de recuperacion no disponible")
	}
	now := a.Now().UTC()
	state, claimed, err := a.Accounts.claimReconciliationSweep(ctx, now)
	if err != nil || !claimed {
		return err
	}
	cutoff := now.Add(-reconciliationRecoveryGrace)
	input := &dynamodb.QueryInput{TableName: &a.Accounts.Table, KeyConditionExpression: aws.String("pk = :pk AND sk BETWEEN :from AND :to"), ExpressionAttributeValues: map[string]types.AttributeValue{
		":pk":   &types.AttributeValueMemberS{Value: reconciliationPendingPK},
		":from": &types.AttributeValueMemberS{Value: "0000"},
		":to":   &types.AttributeValueMemberS{Value: reconciliationJobSK(cutoff, "~")},
	}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(reconciliationRecoveryLimit)}
	if state.RecoveryCursor != "" {
		input.ExclusiveStartKey = key(reconciliationPendingPK, state.RecoveryCursor)
	}
	page, queryErr := db.Query(ctx, input)
	if queryErr != nil {
		return errors.Join(errors.New("query de recuperacion fallida"), a.Accounts.releaseReconciliationSweep(ctx, state, state.RecoveryCursor))
	}
	var failures []error
	for _, raw := range page.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		var job record
		if attributevalue.UnmarshalMap(raw, &job) != nil || !validRecoveryJob(job) {
			failures = append(failures, errors.New("trabajo de recuperacion invalido"))
			continue
		}
		if job.Status != "PENDING" || job.NextAttemptAt > cutoff.Unix() || job.ProviderRetryAt > now.Unix() || job.RecoveryLeaseUntil > now.Unix() {
			continue
		}
		reserved, err := a.Accounts.claimReconciliationRecovery(ctx, job, now, cutoff)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !reserved {
			continue
		}
		body, err := json.Marshal(reconciliationMessage{AttemptID: job.AttemptID, AccountID: job.AccountID, JobSK: job.SK, Count: job.Count})
		if err != nil {
			failures = append(failures, errors.New("mensaje de recuperacion invalido"))
			continue
		}
		_, err = a.Queue.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(a.QueueURL), MessageBody: aws.String(string(body))})
		if err != nil {
			failures = append(failures, errors.New("envio de recuperacion SQS fallido"))
		}
	}
	cursor := ""
	if len(page.LastEvaluatedKey) > 0 {
		last, ok := page.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS)
		if !ok || last.Value == "" {
			return errors.Join(errors.New("cursor de recuperacion invalido"), a.Accounts.releaseReconciliationSweep(ctx, state, state.RecoveryCursor))
		}
		cursor = last.Value
	}
	failures = append(failures, a.Accounts.releaseReconciliationSweep(ctx, state, cursor))
	return errors.Join(failures...)
}

func validRecoveryJob(job record) bool {
	return job.PK == reconciliationPendingPK && validPortalID(job.AttemptID) && validPortalID(job.AccountID) && job.Count >= 0 && job.NextAttemptAt > 0 && job.SK == reconciliationJobSK(time.Unix(job.NextAttemptAt, 0), job.AttemptID)
}

func (s Service) claimReconciliationSweep(ctx context.Context, now time.Time) (record, bool, error) {
	state, err := s.read(ctx, reconciliationRecoveryPK, "STATE")
	condition := "attribute_not_exists(pk)"
	var values map[string]types.AttributeValue
	if errors.Is(err, ErrNotFound) {
		state = record{PK: reconciliationRecoveryPK, SK: "STATE"}
	} else if err != nil {
		return record{}, false, err
	} else {
		if state.RecoveryLeaseUntil > now.Unix() {
			return state, false, nil
		}
		condition = "#version = :previous"
		values = versionValue(state.Version)
	}
	state.Version++
	state.RecoveryLeaseUntil = now.Add(reconciliationSweepLease).Unix()
	w, err := s.put(state, condition, values)
	if err != nil {
		return record{}, false, err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{w}})
	if conditionalRecoveryConflict(err) {
		return state, false, nil
	}
	return state, err == nil, err
}

func (s Service) releaseReconciliationSweep(ctx context.Context, state record, cursor string) error {
	previous := state.Version
	state.Version++
	state.RecoveryCursor = cursor
	state.RecoveryLeaseUntil = 0
	w, err := s.put(state, "#version = :previous", versionValue(previous))
	if err != nil {
		return err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{w}})
	return err
}

func (s Service) claimReconciliationRecovery(ctx context.Context, job record, now, cutoff time.Time) (bool, error) {
	job.RecoveryLeaseUntil = now.Add(reconciliationRecoveryLease).Unix()
	w, err := s.put(job, recoveryJobCondition, map[string]types.AttributeValue{
		":pending": &types.AttributeValueMemberS{Value: "PENDING"},
		":cutoff":  &types.AttributeValueMemberN{Value: strconv.FormatInt(cutoff.Unix(), 10)},
		":now":     &types.AttributeValueMemberN{Value: strconv.FormatInt(now.Unix(), 10)},
	})
	if err != nil {
		return false, err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{w}})
	if conditionalRecoveryConflict(err) {
		return false, nil
	}
	return err == nil, err
}

func conditionalRecoveryConflict(err error) bool {
	var cancelled *types.TransactionCanceledException
	if !errors.As(err, &cancelled) {
		return false
	}
	conditional := false
	for _, reason := range cancelled.CancellationReasons {
		if reason.Code == nil || *reason.Code == "None" {
			continue
		}
		if *reason.Code != "ConditionalCheckFailed" {
			return false
		}
		conditional = true
	}
	return conditional
}
