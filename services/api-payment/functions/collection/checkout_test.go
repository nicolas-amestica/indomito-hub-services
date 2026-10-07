package collection

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestAttemptMustMatchReservedInstallment(t *testing.T) {
	for _, field := range []string{"amount", "installment", "status"} {
		t.Run(field, func(t *testing.T) {
			s, db := serviceFixture(t)
			ctx := context.Background()
			a, err := s.GetAccount(ctx, "account")
			if err != nil {
				t.Fatal(err)
			}
			audit := domain.Audit{CommandID: "reserve", Actor: "session", Reason: "checkout", RecordedAt: time.Now()}
			_, attempt, err := domain.OpenAttempt(a, audit, "attempt", "receipt@example.com")
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "amount":
				attempt.Amount++
			case "installment":
				attempt.InstallmentID = "0002"
			case "status":
				attempt.Status = "PAID"
			}
			_, err = s.Apply(ctx, Command{ID: audit.CommandID, AccountID: a.ID, ExpectedVersion: a.Version, Attempt: &attempt}, func(current domain.Account) (domain.Change, error) {
				change, _, transitionErr := domain.OpenAttempt(current, audit, "attempt", "receipt@example.com")
				return change, transitionErr
			})
			if !errors.Is(err, domain.ErrInvalid) || db.commits != 0 {
				t.Fatalf("inconsistent attempt persisted: %v", err)
			}
		})
	}
}

func TestReserveCheckoutReplayAndLock(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "lost response"}[lost], func(t *testing.T) {
			s, db := serviceFixture(t)
			db.lostResponse = lost
			audit := domain.Audit{CommandID: "reserve", Actor: "public-session", Reason: "checkout requested", RecordedAt: time.Now().UTC()}
			ctx := context.Background()
			first, err := s.ReserveCheckout(ctx, "account", 1, audit, "attempt", paymentOperationID("session"), "receipt@example.com")
			if err != nil || first.Amount != 20000 || first.InstallmentID != "0001" {
				t.Fatalf("reserve: %+v %v", first, err)
			}
			again, err := s.ReserveCheckout(ctx, "account", 1, audit, "attempt", paymentOperationID("session"), "receipt@example.com")
			if err != nil || again != first || db.commits != 1 {
				t.Fatalf("replay: %+v %v", again, err)
			}
			if _, err = s.ReserveCheckout(ctx, "account", 1, audit, "attempt", paymentOperationID("session"), "other@example.com"); !errors.Is(err, ErrReplayMismatch) {
				t.Fatalf("email changed: %v", err)
			}
			audit.CommandID = "another"
			if _, err = s.ReserveCheckout(ctx, "account", 2, audit, "second", paymentOperationID("session"), "receipt@example.com"); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("second checkout: %v", err)
			}
			account, err := s.GetAccount(ctx, "account")
			if err != nil || account.Installments[0].Paid != 0 || account.OpenAttemptID != "attempt" || account.DepositReceived != 0 {
				t.Fatalf("reservation modified cash: %+v %v", account, err)
			}
		})
	}
}

func TestConcurrentCheckoutReservations(t *testing.T) {
	s, db := serviceFixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ReserveCheckout(context.Background(), "account", 1, domain.Audit{CommandID: id, Actor: "session", Reason: "checkout", RecordedAt: time.Now()}, id, paymentOperationID("session"), "receipt@example.com")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, domain.ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 || db.commits != 1 {
		t.Fatalf("success=%d commits=%d", success, db.commits)
	}
}
