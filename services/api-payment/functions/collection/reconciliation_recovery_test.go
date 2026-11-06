package collection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

type recoveryDatabase struct {
	*transactionDB
	statsMu      sync.Mutex
	queries      int
	queryError   error
	afterQuery   func()
	loseJobClaim bool
}

func attributeNumber(value types.AttributeValue) int64 {
	n, err := strconv.ParseInt(value.(*types.AttributeValueMemberN).Value, 10, 64)
	if err != nil {
		panic(err)
	}
	return n
}

func (d *recoveryDatabase) Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	d.statsMu.Lock()
	d.queries++
	err := d.queryError
	callback := d.afterQuery
	d.statsMu.Unlock()
	if in.FilterExpression != nil || in.Limit == nil || *in.Limit != reconciliationRecoveryLimit || in.ExpressionAttributeValues[":pk"].(*types.AttributeValueMemberS).Value != reconciliationPendingPK {
		return nil, errors.New("unbounded recovery query")
	}
	if err != nil {
		return nil, err
	}
	page, err := d.transactionDB.Query(ctx, in, opts...)
	if callback != nil {
		callback()
	}
	return page, err
}

func (d *recoveryDatabase) TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, opts ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	lose := d.loseJobClaim && len(in.TransactItems) == 1 && in.TransactItems[0].Put != nil && *in.TransactItems[0].Put.ConditionExpression == recoveryJobCondition
	if lose {
		d.mu.Lock()
		d.lostResponse = true
		d.mu.Unlock()
	}
	out, err := d.transactionDB.TransactWriteItems(ctx, in, opts...)
	if lose {
		d.mu.Lock()
		d.lostResponse = false
		d.mu.Unlock()
		d.loseJobClaim = false
	}
	var cancelled *types.TransactionCanceledException
	if errors.As(err, &cancelled) {
		cancelled.CancellationReasons = []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}
	}
	return out, err
}

type recoveryQueue struct {
	mu       sync.Mutex
	messages []reconciliationMessage
	fail     bool
	onSend   func(reconciliationMessage)
}

func (q *recoveryQueue) SendMessage(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	var message reconciliationMessage
	if err := json.Unmarshal([]byte(*in.MessageBody), &message); err != nil {
		return nil, err
	}
	q.mu.Lock()
	if q.fail {
		q.mu.Unlock()
		return nil, errors.New("send failed")
	}
	q.messages = append(q.messages, message)
	callback := q.onSend
	q.mu.Unlock()
	if callback != nil {
		callback(message)
	}
	return &sqs.SendMessageOutput{}, nil
}

func recoveryFixture(t *testing.T) (ReconciliationRecoveryApp, *recoveryDatabase, *recoveryQueue, time.Time) {
	t.Helper()
	_, base := serviceFixture(t)
	db := &recoveryDatabase{transactionDB: base}
	queue := &recoveryQueue{}
	now := time.Now().UTC().Truncate(time.Second)
	return ReconciliationRecoveryApp{Accounts: Service{DB: db, Table: "payments"}, Queue: queue, QueueURL: "queue", Now: func() time.Time { return now }}, db, queue, now
}

func recoveryJob(now time.Time, n int) record {
	id := paymentOperationID(fmt.Sprintf("recovery-%d", n))
	due := now.Add(-10 * time.Minute)
	return record{PK: reconciliationPendingPK, SK: reconciliationJobSK(due, id), AttemptID: id, AccountID: approvedID, Status: "PENDING", NextAttemptAt: due.Unix()}
}

func TestRecoveryEmptySweepIsBounded(t *testing.T) {
	a, db, q, _ := recoveryFixture(t)
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if db.queries != 1 || len(q.messages) != 0 || db.commits != 2 {
		t.Fatalf("unexpected idle cost: queries=%d sends=%d writes=%d", db.queries, len(q.messages), db.commits)
	}
}

func TestRecoveryOnlyEnqueuesMatureUnleasedJobs(t *testing.T) {
	a, db, q, now := recoveryFixture(t)
	for n := 0; n < 6; n++ {
		job := recoveryJob(now, n)
		switch n {
		case 1:
			job.NextAttemptAt = now.Add(-time.Minute).Unix()
			job.SK = reconciliationJobSK(time.Unix(job.NextAttemptAt, 0), job.AttemptID)
		case 2:
			job.NextAttemptAt = now.Add(time.Hour).Unix()
			job.SK = reconciliationJobSK(time.Unix(job.NextAttemptAt, 0), job.AttemptID)
		case 3:
			job.RecoveryLeaseUntil = now.Add(time.Minute).Unix()
		case 4:
			job.ProviderRetryAt = now.Add(time.Hour).Unix()
		case 5:
			job.Status = "COMPLETED"
		}
		saveLookupRow(t, db.transactionDB, job)
	}
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(q.messages) != 1 || q.messages[0].AttemptID != recoveryJob(now, 0).AttemptID || q.messages[0].Count != 0 {
		t.Fatalf("wrong recovery: %+v", q.messages)
	}
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(q.messages) != 1 {
		t.Fatal("same job enqueued again during lease")
	}
}

func TestRecoveryConcurrentSweepsDoNotDuplicateSends(t *testing.T) {
	a, db, q, now := recoveryFixture(t)
	saveLookupRow(t, db.transactionDB, recoveryJob(now, 0))
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.Run(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(q.messages) != 1 {
		t.Fatalf("duplicate sends=%d", len(q.messages))
	}
}

func TestRecoveryCursorAdvancesPastDeferredPage(t *testing.T) {
	a, db, q, now := recoveryFixture(t)
	// Los identificadores deterministas se ordenan explícitamente por vencimiento.
	for n := 0; n < 21; n++ {
		job := recoveryJob(now, n)
		job.NextAttemptAt = now.Add(-time.Hour + time.Duration(n)*time.Second).Unix()
		job.SK = reconciliationJobSK(time.Unix(job.NextAttemptAt, 0), job.AttemptID)
		if n < 20 {
			job.ProviderRetryAt = now.Add(time.Hour).Unix()
		}
		saveLookupRow(t, db.transactionDB, job)
	}
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := a.Accounts.read(context.Background(), reconciliationRecoveryPK, "STATE")
	if err != nil || state.RecoveryCursor == "" || len(q.messages) != 0 {
		t.Fatal("first bounded page did not checkpoint")
	}
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(q.messages) != 1 || q.messages[0].AttemptID != recoveryJob(now, 20).AttemptID || db.queries != 2 {
		t.Fatal("deferred jobs starved next page")
	}
	state, err = a.Accounts.read(context.Background(), reconciliationRecoveryPK, "STATE")
	if err != nil || state.RecoveryCursor != "" {
		t.Fatal("cursor did not wrap")
	}
}

func TestRecoveryDoesNotResurrectJobDeletedAfterQuery(t *testing.T) {
	a, db, q, now := recoveryFixture(t)
	job := recoveryJob(now, 0)
	saveLookupRow(t, db.transactionDB, job)
	db.afterQuery = func() { db.mu.Lock(); delete(db.items, job.PK+"/"+job.SK); db.mu.Unlock() }
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(q.messages) != 0 {
		t.Fatal("deleted work re-enqueued")
	}
	if _, err := a.Accounts.read(context.Background(), job.PK, job.SK); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted job resurrected")
	}
}

func TestRecoveryRetriesFailedSendAfterLeaseExpires(t *testing.T) {
	a, db, q, start := recoveryFixture(t)
	now := start
	a.Now = func() time.Time { return now }
	job := recoveryJob(now, 0)
	saveLookupRow(t, db.transactionDB, job)
	q.fail = true
	if err := a.Run(context.Background()); err == nil {
		t.Fatal("send failure hidden")
	}
	q.fail = false
	if err := a.Run(context.Background()); err != nil || len(q.messages) != 0 {
		t.Fatal("ambiguous send retried before lease")
	}
	now = now.Add(15 * time.Minute)
	if err := a.Run(context.Background()); err != nil || len(q.messages) != 1 {
		t.Fatalf("failed send lost: %v", err)
	}
}

func TestRecoveryLostCommitResponsesKeepWork(t *testing.T) {
	for _, jobClaim := range []bool{false, true} {
		t.Run(fmt.Sprint(jobClaim), func(t *testing.T) {
			a, db, q, start := recoveryFixture(t)
			now := start
			a.Now = func() time.Time { return now }
			job := recoveryJob(now, 0)
			saveLookupRow(t, db.transactionDB, job)
			if jobClaim {
				db.loseJobClaim = true
			} else {
				db.lostResponse = true
			}
			if err := a.Run(context.Background()); err == nil {
				t.Fatal("ambiguous claim should report failure")
			}
			db.mu.Lock()
			db.lostResponse = false
			db.mu.Unlock()
			if len(q.messages) != 0 {
				t.Fatal("send authorized after lost commit response")
			}
			now = now.Add(16 * time.Minute)
			if err := a.Run(context.Background()); err != nil || len(q.messages) != 1 {
				t.Fatalf("recovery lost after interrupted claim: %v", err)
			}
		})
	}
}

func TestRecoveryQueryFailurePreservesCursor(t *testing.T) {
	a, db, q, now := recoveryFixture(t)
	saveLookupRow(t, db.transactionDB, recoveryJob(now, 0))
	db.queryError = errors.New("ddb unavailable")
	if err := a.Run(context.Background()); err == nil {
		t.Fatal("query error hidden")
	}
	if len(q.messages) != 0 {
		t.Fatal("enqueued without query")
	}
	db.queryError = nil
	if err := a.Run(context.Background()); err != nil || len(q.messages) != 1 {
		t.Fatal("query failure abandoned work")
	}
}

func TestRecoveryLeaseCannotMaskCapacityErrors(t *testing.T) {
	for _, tc := range []struct {
		code        string
		conditional bool
	}{{"ConditionalCheckFailed", true}, {"TransactionConflict", false}, {"ProvisionedThroughputExceeded", false}} {
		err := &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String(tc.code)}}}
		if conditionalRecoveryConflict(err) != tc.conditional {
			t.Fatalf("wrong classification: %s", tc.code)
		}
	}
	if conditionalRecoveryConflict(&types.TransactionCanceledException{}) {
		t.Fatal("unexplained cancellation swallowed")
	}
}

func TestRecoveryRespectsRetryAfterUntilItsDeadline(t *testing.T) {
	a, db, q, start := recoveryFixture(t)
	now := start
	a.Now = func() time.Time { return now }
	job := recoveryJob(now, 0)
	job.ProviderRetryAt = now.Add(30 * time.Minute).Unix()
	saveLookupRow(t, db.transactionDB, job)
	for _, elapsed := range []time.Duration{0, 15 * time.Minute} {
		now = start.Add(elapsed)
		if err := a.Run(context.Background()); err != nil || len(q.messages) != 0 {
			t.Fatal("Retry-After ignored")
		}
	}
	now = start.Add(30 * time.Minute)
	if err := a.Run(context.Background()); err != nil || len(q.messages) != 1 {
		t.Fatal("due provider retry not recovered")
	}
}

func TestRecoveryLeaseResetsForNextJobGeneration(t *testing.T) {
	a, db, q, created := recoveryFixture(t)
	job := recoveryJob(created, 0)
	job.RecoveryLeaseUntil = created.Add(15 * time.Minute).Unix()
	saveLookupRow(t, db.transactionDB, job)
	next := created.Add(time.Minute)
	if err := a.Accounts.writeRescheduledReconciliation(context.Background(), job, nil, next); err != nil {
		t.Fatal(err)
	}
	newJob, err := a.Accounts.read(context.Background(), reconciliationPendingPK, reconciliationJobSK(next, job.AttemptID))
	if err != nil || newJob.RecoveryLeaseUntil != 0 {
		t.Fatal("previous generation lease leaked")
	}
	a.Now = func() time.Time { return created.Add(10 * time.Minute) }
	if err := a.Run(context.Background()); err != nil || len(q.messages) != 1 || q.messages[0].Count != 1 {
		t.Fatal("new orphan held by old generation lease")
	}
}

func TestRecoveryMalformedRowDoesNotPreventHealthyRecovery(t *testing.T) {
	a, db, q, now := recoveryFixture(t)
	bad := recoveryJob(now, 0)
	bad.AttemptID = "invalid"
	saveLookupRow(t, db.transactionDB, bad)
	saveLookupRow(t, db.transactionDB, recoveryJob(now, 1))
	if err := a.Run(context.Background()); err == nil {
		t.Fatal("malformed job not reported")
	}
	if len(q.messages) != 1 || q.messages[0].AttemptID != recoveryJob(now, 1).AttemptID {
		t.Fatal("malformed row blocked healthy recovery")
	}
}

func TestRecoveryAndConsumerConfirmOnlyOnce(t *testing.T) {
	a, db, q, created := recoveryFixture(t)
	s := a.Accounts
	ctx := context.Background()
	base, err := s.GetAccount(ctx, "account")
	if err != nil {
		t.Fatal(err)
	}
	base.ID = approvedID
	saveLookupRow(t, db.transactionDB, record{PK: "ACCOUNT#" + base.ID, SK: "META", Version: base.Version, Account: &base})
	attemptID := paymentOperationID("recover-real-payment")
	if _, err := s.ReserveCheckout(ctx, base.ID, base.Version, domain.Audit{CommandID: "reserve-recovery", Actor: "session", Reason: "recovery test", RecordedAt: created}, attemptID, paymentOperationID("session"), "receipt@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCheckout(ctx, &checkoutGatewayFake{}, "prd", base.ID, attemptID, "session", testCheckoutURLs(), created); err != nil {
		t.Fatal(err)
	}
	now := created.Add(10 * time.Minute)
	a.Now = func() time.Time { return now }
	v := verifierFake{result: providers.VerifiedPayment{PaymentID: "abcdefghijkl", TransactionID: attemptID, ReceiverID: 123, Amount: "20000", Currency: "CLP", Status: "done", StatusDetail: "normal", ConciliationDate: now}}
	worker := AutomaticReconciliationApp{Accounts: s, Inspector: v, Verifier: v, Now: a.Now, Zone: time.UTC}
	q.onSend = func(msg reconciliationMessage) {
		for range 2 {
			if err := worker.process(ctx, msg); err != nil {
				t.Error(err)
			}
		}
	}
	if err := a.Run(ctx); err != nil {
		t.Fatal(err)
	}
	account, err := s.GetAccount(ctx, base.ID)
	if err != nil || account.Installments[0].Paid != 20000 || account.OpenAttemptID != "" {
		t.Fatal("recovery failed or duplicated payment")
	}
	if len(q.messages) != 1 {
		t.Fatal("wrong recovery count")
	}
}
