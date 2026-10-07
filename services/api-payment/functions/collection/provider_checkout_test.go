package collection

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

type checkoutGatewayFake struct {
	mu                   sync.Mutex
	calls                int
	input                providers.CheckoutRequest
	bankErr, errorCreate error
	url                  string
	afterCreate          func()
}

func (f *checkoutGatewayFake) DevelopmentBank(context.Context) (string, error) {
	return "demo", f.bankErr
}
func (f *checkoutGatewayFake) Create(_ context.Context, in providers.CheckoutRequest) (providers.Checkout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.input = in
	if f.afterCreate != nil {
		f.afterCreate()
	}
	u := f.url
	if u == "" {
		u = "https://khipu.com/payment/test"
	}
	return providers.Checkout{PaymentID: "abcdefghijkl", PaymentURL: u}, f.errorCreate
}
func testCheckoutURLs() CheckoutURLs {
	return CheckoutURLs{Return: "https://example.com/return", Cancel: "https://example.com/cancel", Notify: "https://example.com/webhook"}
}

func TestDevelopmentCheckoutPersistsAndReusesResult(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "lost response"}[lost], func(t *testing.T) {
			s, db := dispatchFixture(t)
			gateway := &checkoutGatewayFake{afterCreate: func() { db.mu.Lock(); db.lostResponse = lost; db.mu.Unlock() }}
			now := time.Now().UTC()
			ctx := context.Background()
			first, err := s.CreateDevelopmentCheckout(ctx, gateway, "account", "attempt", "session", testCheckoutURLs(), now)
			if err != nil {
				t.Fatal(err)
			}
			again, err := s.CreateDevelopmentCheckout(ctx, gateway, "account", "attempt", "session", testCheckoutURLs(), now.Add(time.Minute))
			if err != nil || first != again || gateway.calls != 1 || gateway.input.Amount != 20000 || gateway.input.TransactionID != "attempt" || gateway.input.BankID != "demo" {
				t.Fatalf("replay %+v %v calls=%d", again, err, gateway.calls)
			}
			a, err := s.GetAccount(ctx, "account")
			if err != nil || a.Installments[0].Paid != 0 || a.OpenAttemptID != "attempt" {
				t.Fatalf("cash changed %+v %v", a, err)
			}
			if _, err = s.CreateDevelopmentCheckout(ctx, gateway, "account", "attempt", "session", testCheckoutURLs(), now.Add(2*time.Hour)); !errors.Is(err, ErrDispatchUncertain) || gateway.calls != 1 {
				t.Fatalf("expired checkout recreated: %v", err)
			}
		})
	}
}

func TestDevelopmentCheckoutAmbiguityDoesNotRepeatCreate(t *testing.T) {
	for _, scenario := range []string{"provider error", "unsafe url", "storage failure", "claim response lost"} {
		t.Run(scenario, func(t *testing.T) {
			s, db := dispatchFixture(t)
			gateway := &checkoutGatewayFake{}
			expectedCalls := 1
			switch scenario {
			case "provider error":
				gateway.errorCreate = errors.New("timeout")
			case "unsafe url":
				gateway.url = "https://untrusted.example/pay"
			case "storage failure":
				gateway.afterCreate = func() { db.mu.Lock(); db.failOnCommit = 3; db.mu.Unlock() }
			case "claim response lost":
				db.lostResponse = true
				expectedCalls = 0
			}
			for range 2 {
				if _, err := s.CreateDevelopmentCheckout(context.Background(), gateway, "account", "attempt", "session", testCheckoutURLs(), time.Now()); !errors.Is(err, ErrDispatchUncertain) {
					t.Fatalf("unexpected result %v", err)
				}
			}
			if gateway.calls != expectedCalls {
				t.Fatalf("calls=%d", gateway.calls)
			}
		})
	}
}

func TestDevelopmentCheckoutConcurrentCreation(t *testing.T) {
	s, _ := dispatchFixture(t)
	gateway := &checkoutGatewayFake{}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateDevelopmentCheckout(context.Background(), gateway, "account", "attempt", "session", testCheckoutURLs(), time.Now())
			if err != nil && !errors.Is(err, ErrDispatchUncertain) {
				t.Errorf("unexpected error %v", err)
			}
		}()
	}
	wg.Wait()
	if gateway.calls != 1 {
		t.Fatalf("duplicate create calls=%d", gateway.calls)
	}
}

func TestDevelopmentCheckoutRejectsNonDemoBeforeClaim(t *testing.T) {
	s, db := dispatchFixture(t)
	gateway := &checkoutGatewayFake{bankErr: providers.ErrVerification}
	if _, err := s.CreateDevelopmentCheckout(context.Background(), gateway, "account", "attempt", "session", testCheckoutURLs(), time.Now()); !errors.Is(err, providers.ErrVerification) || gateway.calls != 0 || db.commits != 1 {
		t.Fatalf("demo gate bypassed %v", err)
	}
}
