package collection

import (
	"context"
	"testing"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func reconciliationAudit() domain.Audit {
	return domain.Audit{CommandID: paymentOperationID("admin-reconcile"), Actor: "operator", Reason: "Estado autoritativo revisado", RecordedAt: time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)}
}

func TestReconcileAttemptNeverReleasesPendingProviderPayment(t *testing.T) {
	s, _, v := confirmationFixture(t)
	v.result.Status = "verifying"
	v.result.StatusDetail = "pending"
	v.result.ExpiresDate = time.Date(2026, 10, 5, 16, 0, 0, 0, time.UTC)
	state, err := s.ReconcileProviderAttempt(context.Background(), v, v, "attempt", "abcdefghijkl", reconciliationAudit(), time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC), time.UTC)
	if err != nil || state.Status != "PENDING" {
		t.Fatalf("pending: %+v %v", state, err)
	}
	account, _ := s.GetAccount(context.Background(), "account")
	if account.OpenAttemptID != "attempt" {
		t.Fatal("pending attempt released")
	}
}

func TestReconcileAttemptReleasesOnlyExpiredAuthoritativeUnpaid(t *testing.T) {
	s, db, v := confirmationFixture(t)
	v.result.Status = "pending"
	v.result.StatusDetail = "rejected-by-payer"
	v.result.ExpiresDate = time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	state, err := s.ReconcileProviderAttempt(context.Background(), v, v, "attempt", "abcdefghijkl", reconciliationAudit(), time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC), time.UTC)
	if err != nil || state.Status != "UNPAID_FINAL" {
		t.Fatalf("unpaid: %+v %v", state, err)
	}
	commits := db.commits
	state, err = s.ReconcileProviderAttempt(context.Background(), v, v, "attempt", "abcdefghijkl", reconciliationAudit(), time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC), time.UTC)
	if err != nil || state.Status != "UNPAID_FINAL" || db.commits != commits {
		t.Fatal("unpaid replay changed state")
	}
	account, _ := s.GetAccount(context.Background(), "account")
	if account.OpenAttemptID != "" || account.Installments[0].Paid != 0 {
		t.Fatal("unpaid resolution changed money")
	}
}

func TestReconcileDeletedPayment(t *testing.T) {
	for _, scenario := range []string{"deleted unpaid", "already received", "provider conciliation"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, v := confirmationFixture(t)
			ctx := context.Background()
			now := time.Now().UTC()
			if scenario == "already received" {
				if _, err := s.ConfirmProviderCheckout(ctx, v, "attempt", "abcdefghijkl", now, time.UTC); err != nil {
					t.Fatal(err)
				}
			}
			v.result.Status, v.result.StatusDetail = "deleted", "pending"
			v.result.ConciliationDate = time.Time{}
			v.result.ExpiresDate = now.Add(time.Hour)
			if scenario == "provider conciliation" {
				v.result.ConciliationDate = now
			}
			state, err := s.ReconcileProviderAttempt(ctx, v, v, "attempt", "abcdefghijkl", reconciliationAudit(), now, time.UTC)
			want := "REVIEW_REQUIRED"
			if scenario == "deleted unpaid" {
				want = "UNPAID_FINAL"
			}
			if err != nil || state.Status != want {
				t.Fatalf("state=%+v err=%v", state, err)
			}
			account, err := s.GetAccount(ctx, "account")
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "deleted unpaid" {
				if account.OpenAttemptID != "" || account.Installments[0].Paid != 0 {
					t.Fatal("deleted attempt not released safely")
				}
				commits := db.commits
				if _, err := s.ReconcileProviderAttempt(ctx, v, v, "attempt", "", reconciliationAudit(), now, time.UTC); err != nil || db.commits != commits {
					t.Fatal("replay changed resolved payment")
				}
			} else if scenario == "already received" && account.Installments[0].Paid != 20000 {
				t.Fatal("deletion reversed received funds")
			} else if scenario == "provider conciliation" && account.OpenAttemptID != "attempt" {
				t.Fatal("contradictory payment released")
			}
		})
	}
}

func TestReconcileProviderReversalReopensExactlyOriginalDebt(t *testing.T) {
	s, db, v := confirmationFixture(t)
	now := time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)
	state, err := s.ReconcileProviderAttempt(context.Background(), v, v, "attempt", "abcdefghijkl", reconciliationAudit(), now, time.UTC)
	if err != nil || state.Status != "CONFIRMED" {
		t.Fatal(err)
	}
	v.result.StatusDetail = "reversed"
	state, err = s.ReconcileProviderAttempt(context.Background(), v, v, "attempt", "abcdefghijkl", reconciliationAudit(), now.Add(time.Hour), time.UTC)
	if err != nil || state.Status != "REVERSED" {
		t.Fatalf("reversal: %+v %v", state, err)
	}
	commits := db.commits
	state, err = s.ReconcileProviderAttempt(context.Background(), v, v, "attempt", "abcdefghijkl", reconciliationAudit(), now.Add(time.Hour), time.UTC)
	if err != nil || state.Status != "REVERSED" || db.commits != commits {
		t.Fatal("reversal replay changed state")
	}
	account, _ := s.GetAccount(context.Background(), "account")
	if account.Installments[0].Paid != 0 {
		t.Fatal("reversed payment remained paid")
	}
}

func TestSettledProviderReversalRequiresBankReview(t *testing.T) {
	s, _, v := confirmationFixture(t)
	now := time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)
	state, err := s.ReconcileProviderAttempt(context.Background(), v, v, "attempt", "abcdefghijkl", reconciliationAudit(), now, time.UTC)
	if err != nil || state.Status != "CONFIRMED" {
		t.Fatal(err)
	}
	settlementAudit := treasuryAudit("settled-before-reversal")
	if _, _, err = s.ReconcileSettlement(context.Background(), "trip", "khipu:abcdefghijkl", 1, 20000, 500, "bank:settled-before-reversal", "2026-10-05", settlementAudit); err != nil {
		t.Fatal(err)
	}
	v.result.StatusDetail = "reversed"
	state, err = s.ReconcileProviderAttempt(context.Background(), v, v, "attempt", "abcdefghijkl", reconciliationAudit(), now.Add(time.Hour), time.UTC)
	if err != nil || state.Status != "REVIEW_REQUIRED" {
		t.Fatalf("settled reversal was not held: %+v %v", state, err)
	}
	account, err := s.GetAccount(context.Background(), "account")
	if err != nil || account.Installments[0].Paid != 20000 {
		t.Fatal("settled bank funds were reversed without treasury evidence")
	}
}

func TestMarkedPaidByReceiverRequiresReviewWithoutBookingMoney(t *testing.T) {
	s, _, v := confirmationFixture(t)
	v.result.Status = "done"
	v.result.StatusDetail = "marked-paid-by-receiver"
	state, err := s.ReconcileProviderAttempt(context.Background(), v, v, "attempt", "abcdefghijkl", reconciliationAudit(), time.Now(), time.UTC)
	if err != nil || state.Status != "REVIEW_REQUIRED" {
		t.Fatal(err)
	}
	account, _ := s.GetAccount(context.Background(), "account")
	if account.Installments[0].Paid != 0 || account.OpenAttemptID != "attempt" {
		t.Fatal("manual provider mark booked as cash")
	}
}
