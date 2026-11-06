package collection

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestMissingCheckoutKeepsWorkAndEmitsOperationalAlert(t *testing.T) {
	for _, scenario := range []string{"provider response lost", "storage failure", "claim response lost"} {
		t.Run(scenario, func(t *testing.T) {
			s, db := dispatchFixture(t)
			ctx := context.Background()
			created := time.Now().UTC()
			gateway := &checkoutGatewayFake{}
			switch scenario {
			case "provider response lost":
				gateway.errorCreate = errors.New("timeout after provider creation")
			case "storage failure":
				gateway.afterCreate = func() { db.failOnCommit = 3 }
			case "claim response lost":
				db.lostResponse = true
			}
			if _, err := s.CreateCheckout(ctx, gateway, "prd", "account", "attempt", "session", testCheckoutURLs(), created); !errors.Is(err, ErrDispatchUncertain) {
				t.Fatal(err)
			}
			db.lostResponse, db.failOnCommit = false, 0
			now := created.Add(time.Minute)
			core, logs := observer.New(zap.WarnLevel)
			worker := AutomaticReconciliationApp{Accounts: s, Now: func() time.Time { return now }, Log: zap.New(core), Stage: "prd"}
			message := reconciliationMessage{AttemptID: "attempt", AccountID: "account", JobSK: reconciliationJobSK(now, "attempt")}
			if err := worker.process(ctx, message); err != nil {
				t.Fatal(err)
			}
			view, err := s.read(ctx, "ATTEMPT#attempt", "PROVIDER_STATUS")
			if err != nil || view.Status != "PROVIDER_REVIEW_REQUIRED" || view.ProviderDetail != "CHECKOUT_RESPONSE_MISSING" {
				t.Fatalf("missing review: %+v %v", view, err)
			}
			if _, err := s.read(ctx, reconciliationPendingPK, reconciliationJobSK(now.Add(2*time.Minute), "attempt")); err != nil {
				t.Fatalf("work abandoned: %v", err)
			}
			if logs.Len() != 1 || logs.All()[0].ContextMap()["CheckoutRecoveryRequired"] != int64(1) {
				t.Fatal("missing operational metric")
			}
			account, err := s.GetAccount(ctx, "account")
			if err != nil || account.OpenAttemptID != "attempt" || account.Installments[0].Paid != 0 {
				t.Fatal("financial state changed")
			}
			calls := gateway.calls
			if _, err := s.CreateCheckout(ctx, gateway, "prd", "account", "attempt", "session", testCheckoutURLs(), now); !errors.Is(err, ErrDispatchUncertain) || gateway.calls != calls {
				t.Fatal("ambiguous POST repeated")
			}
		})
	}
}

func TestMissingCheckoutFinishesFromVerifiedOutcomeOrResolution(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "webhook confirmation", true: "admin deleted resolution"}[deleted], func(t *testing.T) {
			ctx := context.Background()
			s, db, v := confirmationFixture(t)
			var job record
			for _, item := range db.items {
				if err := attributevalue.UnmarshalMap(item, &job); err == nil && job.PK == reconciliationPendingPK {
					break
				}
			}
			if job.PK != reconciliationPendingPK {
				t.Fatal("missing pre-dispatch job")
			}
			now := time.Now().UTC()
			want := "CONFIRMED"
			if deleted {
				v.result.Status, v.result.StatusDetail = "deleted", "pending"
				v.result.ConciliationDate = time.Time{}
				want = "UNPAID_FINAL"
				if _, err := s.ReconcileProviderAttempt(ctx, v, v, "attempt", "abcdefghijkl", reconciliationAudit(), now, time.UTC); err != nil {
					t.Fatal(err)
				}
			} else if _, err := s.ConfirmProviderCheckout(ctx, v, "attempt", "abcdefghijkl", now, time.UTC); err != nil {
				t.Fatal(err)
			}
			worker := AutomaticReconciliationApp{Accounts: s, Now: func() time.Time { return now }}
			msg := reconciliationMessage{AttemptID: "attempt", AccountID: "account", JobSK: job.SK, Count: job.Count}
			commits := db.commits
			for range 2 {
				if err := worker.process(ctx, msg); err != nil {
					t.Fatal(err)
				}
			}
			if db.commits != commits+1 {
				t.Fatal("duplicated terminal processing")
			}
			if _, err := s.read(ctx, reconciliationPendingPK, job.SK); !errors.Is(err, ErrNotFound) {
				t.Fatal("terminal job retained")
			}
			observation, err := s.read(ctx, "ATTEMPT#attempt", "PROVIDER_STATUS")
			if err != nil || observation.Status != want {
				t.Fatalf("observation=%+v err=%v", observation, err)
			}
		})
	}
}

func TestCheckoutResponseCanArriveAfterRecoveryStarted(t *testing.T) {
	ctx := context.Background()
	s, _ := dispatchFixture(t)
	created := time.Now().UTC()
	now := created.Add(time.Minute)
	worker := AutomaticReconciliationApp{Accounts: s, Now: func() time.Time { return now }}
	initial := reconciliationMessage{AttemptID: "attempt", AccountID: "account", JobSK: reconciliationJobSK(now, "attempt")}
	gateway := &checkoutGatewayFake{beforeCreate: func() {
		if err := worker.process(ctx, initial); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := s.CreateCheckout(ctx, gateway, "prd", "account", "attempt", "session", testCheckoutURLs(), created); err != nil {
		t.Fatal(err)
	}
	job, err := s.read(ctx, reconciliationPendingPK, reconciliationJobSK(now.Add(2*time.Minute), "attempt"))
	if err != nil || job.Count != 1 {
		t.Fatal("late response reset recovery")
	}
	// El siguiente trabajo debe usar el CHECKOUT recuperado y confirmar una vez.
	_, _, v := confirmationFixture(t)
	worker.Inspector, worker.Verifier, worker.Zone = v, v, time.UTC
	now = now.Add(2 * time.Minute)
	if err := worker.process(ctx, reconciliationMessage{AttemptID: "attempt", AccountID: "account", JobSK: job.SK, Count: job.Count}); err != nil {
		t.Fatal(err)
	}
	account, err := s.GetAccount(ctx, "account")
	if err != nil || account.Installments[0].Paid != 20000 || gateway.calls != 1 {
		t.Fatal("late checkout failed or duplicated creation")
	}
}
