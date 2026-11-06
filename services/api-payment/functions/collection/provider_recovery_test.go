package collection

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

type recoveringInspector struct {
	verifierFake
	calls int
}

func (v *recoveringInspector) InspectPayment(ctx context.Context, id, tx string) (providers.VerifiedPayment, error) {
	v.calls++
	return v.verifierFake.InspectPayment(ctx, id, tx)
}

func providerRecoveryFixture(t *testing.T) (Service, *transactionDB, AutomaticReconciliationApp, *recoveringInspector, reconciliationMessage, time.Time) {
	t.Helper()
	s, db := dispatchFixture(t)
	created := time.Now().UTC().Truncate(time.Second)
	if _, err := s.CreateCheckout(context.Background(), &checkoutGatewayFake{}, "prd", "account", "attempt", "session", testCheckoutURLs(), created); err != nil {
		t.Fatal(err)
	}
	_, _, base := confirmationFixture(t)
	v := &recoveringInspector{verifierFake: base}
	now := created.Add(time.Minute)
	worker := AutomaticReconciliationApp{Accounts: s, Inspector: v, Verifier: v, Zone: time.UTC, Now: func() time.Time { return now }}
	return s, db, worker, v, reconciliationMessage{AttemptID: "attempt", AccountID: "account", JobSK: reconciliationJobSK(now, "attempt")}, now
}

func TestProviderFailuresKeepDurableWorkAndAccountBlocked(t *testing.T) {
	for _, tc := range []struct {
		name, code, status string
		http               int
		retryable          bool
	}{
		{"credentials", "AUTHENTICATION_FAILED", "PROVIDER_REVIEW_REQUIRED", 401, false},
		{"permissions", "PERMISSION_DENIED", "PROVIDER_REVIEW_REQUIRED", 403, false},
		{"invalid request", "INVALID_REQUEST", "PROVIDER_REVIEW_REQUIRED", 400, false},
		{"missing payment", "PAYMENT_NOT_FOUND", "PROVIDER_REVIEW_REQUIRED", 404, false},
		{"invalid JSON", "INVALID_RESPONSE", "PROVIDER_REVIEW_REQUIRED", 200, false},
		{"identity mismatch", "VERIFICATION_FAILED", "PROVIDER_REVIEW_REQUIRED", 200, false},
		{"network", "NETWORK_ERROR", "RECONCILIATION_REQUIRED", 0, true},
		{"timeout", "REQUEST_TIMEOUT", "RECONCILIATION_REQUIRED", 0, true},
		{"rate limit", "RATE_LIMITED", "RECONCILIATION_REQUIRED", 429, true},
		{"server down", "SERVICE_UNAVAILABLE", "RECONCILIATION_REQUIRED", 503, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, worker, v, msg, now := providerRecoveryFixture(t)
			core, logs := observer.New(zap.WarnLevel)
			worker.Log, worker.Stage = zap.New(core), "prd"
			v.err = &providers.KhipuError{Code: tc.code, HTTPStatus: tc.http, Retryable: tc.retryable}
			if tc.code == "VERIFICATION_FAILED" {
				v.err = providers.ErrVerification
			}
			if err := worker.process(context.Background(), msg); err != nil {
				t.Fatal(err)
			}
			delay := 2 * time.Minute
			if !tc.retryable {
				delay = 15 * time.Minute
			}
			job, err := s.read(context.Background(), reconciliationPendingPK, reconciliationJobSK(now.Add(delay), "attempt"))
			if err != nil || job.ProviderFailures != 1 || job.Count != 1 {
				t.Fatalf("work lost: %+v %v", job, err)
			}
			observation, err := s.read(context.Background(), "ATTEMPT#attempt", "PROVIDER_STATUS")
			if err != nil || observation.Status != tc.status || observation.ProviderDetail != tc.code {
				t.Fatalf("observation=%+v err=%v", observation, err)
			}
			account, err := s.GetAccount(context.Background(), "account")
			if err != nil || account.OpenAttemptID != "attempt" || account.Installments[0].Paid != 0 {
				t.Fatal("provider error released or paid quota")
			}
			if _, err := s.read(context.Background(), reconciliationPendingPK, msg.JobSK); !errors.Is(err, ErrNotFound) {
				t.Fatal("old job retained")
			}
			if logs.Len() != 1 {
				t.Fatal("missing diagnostic")
			}
			if _, alert := logs.All()[0].ContextMap()["ProviderRecoveryRequired"]; alert == tc.retryable {
				t.Fatal("incorrect immediate alert")
			}
		})
	}
}

func TestRetryAfterAndCredentialProbeDoNotQueryEarly(t *testing.T) {
	for _, credentials := range []bool{false, true} {
		t.Run(map[bool]string{false: "Retry-After", true: "credentials"}[credentials], func(t *testing.T) {
			s, db, worker, v, msg, start := providerRecoveryFixture(t)
			now := start
			worker.Now = func() time.Time { return now }
			v.err = &providers.KhipuError{Code: "RATE_LIMITED", HTTPStatus: 429, Retryable: true, RetryAt: now.Add(time.Hour)}
			if credentials {
				v.err = &providers.KhipuError{Code: "AUTHENTICATION_FAILED", HTTPStatus: 401}
			}
			if err := worker.process(context.Background(), msg); err != nil {
				t.Fatal(err)
			}
			checked := now.Unix()
			for n := 1; n <= 3; n++ {
				now = start.Add(time.Duration(n) * 15 * time.Minute)
				msg.JobSK, msg.Count = reconciliationJobSK(now, "attempt"), int64(n)
				if err := worker.process(context.Background(), msg); err != nil {
					t.Fatal(err)
				}
				observation, err := s.read(context.Background(), "ATTEMPT#attempt", "PROVIDER_STATUS")
				if err != nil || observation.CheckedAt != checked || v.calls != 1 {
					t.Fatal("early delivery queried or changed last check")
				}
			}
			now = start.Add(time.Hour)
			msg.JobSK, msg.Count = reconciliationJobSK(now, "attempt"), 4
			v.err = nil
			v.result.ConciliationDate = now
			for range 2 {
				if err := worker.process(context.Background(), msg); err != nil {
					t.Fatal(err)
				}
			}
			account, err := s.GetAccount(context.Background(), "account")
			if err != nil || account.Installments[0].Paid != 20000 || account.OpenAttemptID != "" || v.calls != 2 {
				t.Fatal("provider recovery failed or duplicated money")
			}
			if db.items[reconciliationPendingPK+"/"+msg.JobSK] != nil {
				t.Fatal("confirmed job retained")
			}
		})
	}
}

func TestOutageLongerThanSQSRetryLimitKeepsWorking(t *testing.T) {
	s, _, worker, v, msg, start := providerRecoveryFixture(t)
	now := start
	worker.Now = func() time.Time { return now }
	core, logs := observer.New(zap.WarnLevel)
	worker.Log = zap.New(core)
	v.err = &providers.KhipuError{Code: "SERVICE_UNAVAILABLE", HTTPStatus: 500, Retryable: true}
	for i := 0; i < 8; i++ {
		if err := worker.process(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
		now = now.Add(automaticReconciliationDelay(int64(i + 1)))
		msg.JobSK, msg.Count = reconciliationJobSK(now, "attempt"), int64(i+1)
	}
	job, err := s.read(context.Background(), reconciliationPendingPK, msg.JobSK)
	if err != nil || job.ProviderFailures != 8 || v.calls != 8 {
		t.Fatal("outage exhausted recovery")
	}
	if logs.FilterField(zap.Int("ProviderRecoveryRequired", 1)).Len() != 6 {
		t.Fatal("prolonged outage not alerted after three failures")
	}
	v.err = nil
	v.result.Status, v.result.StatusDetail = "pending", "pending"
	if err := worker.process(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	job, err = s.read(context.Background(), reconciliationPendingPK, reconciliationJobSK(now.Add(15*time.Minute), "attempt"))
	if err != nil || job.ProviderFailures != 0 || job.ProviderRetryAt != 0 {
		t.Fatal("successful consultation did not reset failures")
	}
}

func TestPersistenceAndCallerFailuresStillFailTheMessage(t *testing.T) {
	for _, scenario := range []string{"database write", "caller cancellation"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, worker, v, msg, _ := providerRecoveryFixture(t)
			if scenario == "database write" {
				v.err = &providers.KhipuError{Code: "NETWORK_ERROR", Retryable: true}
				db.failOnCommit = db.commits + 1
			} else {
				v.err = context.Canceled
			}
			if err := worker.process(context.Background(), msg); err == nil {
				t.Fatal("internal failure acknowledged")
			}
			if _, err := s.read(context.Background(), reconciliationPendingPK, msg.JobSK); err != nil {
				t.Fatal("failed transaction discarded work")
			}
		})
	}
}

func TestKnownPaymentClosesRecoveryEvenIfProviderIsUnavailable(t *testing.T) {
	s, _, worker, v, msg, now := providerRecoveryFixture(t)
	if _, err := s.ConfirmProviderCheckout(context.Background(), v, "attempt", "abcdefghijkl", now, time.UTC); err != nil {
		t.Fatal(err)
	}
	v.err = &providers.KhipuError{Code: "PAYMENT_NOT_FOUND", HTTPStatus: 404}
	if err := worker.process(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.read(context.Background(), reconciliationPendingPK, msg.JobSK); !errors.Is(err, ErrNotFound) {
		t.Fatal("confirmed recovery remains open")
	}
}

func TestWebhookConfirmationClosesWorkDuringRetryAfterWait(t *testing.T) {
	s, _, worker, v, msg, start := providerRecoveryFixture(t)
	now := start
	worker.Now = func() time.Time { return now }
	failure := &providers.KhipuError{Code: "RATE_LIMITED", HTTPStatus: 429, Retryable: true, RetryAt: now.Add(time.Hour)}
	v.err = failure
	if err := worker.process(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	v.err = nil
	if _, err := s.ConfirmProviderCheckout(context.Background(), v, "attempt", "abcdefghijkl", now, time.UTC); err != nil {
		t.Fatal(err)
	}
	v.err = failure
	now = start.Add(15 * time.Minute)
	msg.JobSK, msg.Count = reconciliationJobSK(now, "attempt"), 1
	if err := worker.process(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if v.calls != 1 {
		t.Fatal("queried provider before Retry-After despite local confirmation")
	}
	if _, err := s.read(context.Background(), reconciliationPendingPK, msg.JobSK); !errors.Is(err, ErrNotFound) {
		t.Fatal("confirmed work held until Retry-After")
	}
}
