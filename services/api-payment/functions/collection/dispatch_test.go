package collection

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"sync"
	"testing"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestDispatchRevalidatesAccount(t *testing.T) {
	for _, change := range []string{"inactive", "unlocked", "amount"} {
		t.Run(change, func(t *testing.T) {
			s, db := dispatchFixture(t)
			ctx := context.Background()
			a, err := s.GetAccount(ctx, "account")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "inactive":
				a.Active = false
			case "unlocked":
				a.OpenAttemptID = ""
			case "amount":
				a.Installments[0].Discount = 100
			}
			a.Version++
			item, err := attributevalue.MarshalMap(record{PK: "ACCOUNT#account", SK: "META", Version: a.Version, Account: &a})
			if err != nil {
				t.Fatal(err)
			}
			db.items[itemKey(item)] = item
			if _, err = s.ClaimCheckoutDispatch(ctx, "account", "attempt", "session", time.Now()); err == nil || db.commits != 1 {
				t.Fatalf("changed account dispatched: %v", err)
			}
		})
	}
}

func dispatchFixture(t *testing.T) (Service, *transactionDB) {
	t.Helper()
	s, db := serviceFixture(t)
	_, err := s.ReserveCheckout(context.Background(), "account", 1, domain.Audit{CommandID: "reserve", Actor: "session", Reason: "checkout", RecordedAt: time.Now()}, "attempt", paymentOperationID("session"), "receipt@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func TestDispatchOnlyOneCallerAuthorized(t *testing.T) {
	s, db := dispatchFixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ClaimCheckoutDispatch(context.Background(), "account", "attempt", "session", time.Now())
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrDispatchUncertain) {
			t.Fatal(err)
		}
	}
	if success != 1 || db.commits != 2 {
		t.Fatalf("authorizations=%d commits=%d", success, db.commits)
	}
	a, err := s.GetAccount(context.Background(), "account")
	if err != nil || a.Version != 2 || a.Installments[0].Paid != 0 || a.OpenAttemptID != "attempt" {
		t.Fatalf("financial state changed: %+v %v", a, err)
	}
	r, err := s.read(context.Background(), "ATTEMPT#attempt", "DISPATCH")
	if err != nil || r.ExpiresAt != 0 || r.Event == nil || r.Event.Actor != "session" {
		t.Fatalf("missing permanent audit: %+v %v", r, err)
	}
}

func TestDispatchLostResponseNeverAuthorizesRetry(t *testing.T) {
	s, db := dispatchFixture(t)
	db.lostResponse = true
	for range 2 {
		_, err := s.ClaimCheckoutDispatch(context.Background(), "account", "attempt", "session", time.Now())
		if !errors.Is(err, ErrDispatchUncertain) {
			t.Fatalf("ambiguous send authorized: %v", err)
		}
	}
	if db.commits != 2 {
		t.Fatalf("duplicate claim: %d", db.commits)
	}
}

func TestDispatchRejectsInvalidOwnershipAndInput(t *testing.T) {
	for _, tc := range []struct {
		name, account, attempt, actor string
		now                           time.Time
	}{
		{"other account", "other", "attempt", "session", time.Now()},
		{"unknown attempt", "account", "unknown", "session", time.Now()},
		{"missing actor", "account", "attempt", "", time.Now()},
		{"missing timestamp", "account", "attempt", "session", time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := dispatchFixture(t)
			if _, err := s.ClaimCheckoutDispatch(context.Background(), tc.account, tc.attempt, tc.actor, tc.now); err == nil || db.commits != 1 {
				t.Fatalf("invalid dispatch accepted: %v", err)
			}
		})
	}
}
