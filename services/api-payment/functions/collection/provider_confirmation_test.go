package collection

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

func TestProviderAmountMismatchRecordsActualFundsOnce(t *testing.T) {
	for _, amount := range []int64{10000, 25000} {
		t.Run(strconv.FormatInt(amount, 10), func(t *testing.T) {
			s, db, v := confirmationFixture(t)
			v.result.Amount = strconv.FormatInt(amount, 10)
			for range 2 {
				c, err := s.ConfirmProviderCheckout(context.Background(), v, "attempt", "abcdefghijkl", time.Now(), time.UTC)
				if err != nil || c.Event.Type != "PAYMENT_REQUIRES_REVIEW" || c.Event.Amount != amount || c.Account.UnappliedReceived != amount || c.Account.Installments[0].Paid != 0 || c.Account.Installments[0].Outstanding() != 20000 {
					t.Fatalf("incorrect mismatch %+v %v", c, err)
				}
				if c.Event.Entries[0].Amount != amount || c.Event.Entries[1].Amount != -amount || c.Event.Entries[1].Account != "UNAPPLIED_FUNDS" {
					t.Fatalf("unbalanced received funds %+v", c.Event.Entries)
				}
			}
			if db.commits != 3 {
				t.Fatalf("duplicate receipt/ledger commits=%d", db.commits)
			}
		})
	}
}

func TestConfirmedOutcomeSurvivesMissingCheckoutAndDistinctReceipts(t *testing.T) {
	s, db, v := confirmationFixture(t)
	ctx := context.Background()
	app := PortalApp{Accounts: s, Now: time.Now}
	for index, paymentID := range []string{"abcdefghijkl", "mnopqrstuvwx"} {
		v.result.PaymentID = paymentID
		if _, err := s.ConfirmProviderCheckout(ctx, v, "attempt", paymentID, time.Now(), time.UTC); err != nil {
			t.Fatal(err)
		}
		a, err := s.GetAccount(ctx, "account")
		if err != nil {
			t.Fatal(err)
		}
		view, err := app.attemptView(ctx, a, "attempt")
		want := "CONFIRMED"
		if index == 1 {
			want = "REVIEW_REQUIRED"
		}
		if err != nil || view.Status != want || view.PaymentURL != "" {
			t.Fatalf("lost outcome %+v %v", view, err)
		}
	}
	a, err := s.GetAccount(ctx, "account")
	if err != nil || a.Installments[0].Paid != 20000 || a.UnappliedReceived != 20000 || db.commits != 4 {
		t.Fatal("lost distinct real receipt")
	}
}

type verifierFake struct {
	result providers.VerifiedPayment
	err    error
}

func (v verifierFake) VerifyReceived(context.Context, string, string) (providers.VerifiedPayment, error) {
	return v.result, v.err
}
func (v verifierFake) InspectPayment(context.Context, string, string) (providers.VerifiedPayment, error) {
	return v.result, v.err
}
func confirmationFixture(t *testing.T) (Service, *transactionDB, verifierFake) {
	t.Helper()
	s, db := dispatchFixture(t)
	if _, err := s.ClaimCheckoutDispatch(context.Background(), "account", "attempt", "session", time.Now()); err != nil {
		t.Fatal(err)
	}
	return s, db, verifierFake{result: providers.VerifiedPayment{PaymentID: "abcdefghijkl", TransactionID: "attempt", ReceiverID: 123, Amount: "20000.0000", Currency: "CLP", Status: "done", StatusDetail: "normal", ConciliationDate: time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)}}
}

func TestProviderConfirmationExactlyOnce(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "concurrent", true: "lost response"}[lost], func(t *testing.T) {
			s, db, v := confirmationFixture(t)
			db.lostResponse = lost
			zone := time.FixedZone("business", -3*3600)
			var wg sync.WaitGroup
			for range 20 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					c, err := s.ConfirmProviderCheckout(context.Background(), v, "attempt", "abcdefghijkl", time.Now(), zone)
					if err != nil || c.Event.EffectiveDate != "2026-09-30" {
						t.Errorf("confirmation %v %+v", err, c.Event)
					}
				}()
			}
			wg.Wait()
			a, err := s.GetAccount(context.Background(), "account")
			if err != nil || a.Installments[0].Paid != 20000 || a.OpenAttemptID != "" || db.commits != 3 {
				t.Fatalf("duplicate cash %+v commits=%d %v", a, db.commits, err)
			}
			c, err := s.confirmedPayment(context.Background(), paymentOperationID("khipu:abcdefghijkl"), "account", "khipu:abcdefghijkl")
			if err != nil || c.Event.Entries[0].Account != "PROVIDER_RECEIVABLE" {
				t.Fatalf("incorrect bank entry %+v %v", c, err)
			}
			if db.items["RECEIPT#"+paymentOperationID("khipu:abcdefghijkl")+"/META"] == nil {
				t.Fatal("missing receipt outbox metadata")
			}
			taxID := paymentOperationID("khipu:abcdefghijkl")
			tax, taxErr := s.read(context.Background(), "TAX_REQUEST#"+taxID, "META")
			if taxErr != nil || tax.TaxRequest == nil || tax.TaxRequest.Status != taxStatusPending || tax.TaxRequest.GrossAmount != 20000 || tax.TaxRequest.SourceEventID != taxID {
				t.Fatalf("missing idempotent tax request: %+v %v", tax.TaxRequest, taxErr)
			}
			if db.items["TAX_QUEUE#PENDING/"+tax.TaxRequest.RequestedAt.UTC().Format(time.RFC3339Nano)+"#"+taxID] == nil {
				t.Fatal("missing tax pending projection")
			}
			settlement, settlementErr := s.read(context.Background(), "TRIP#trip", "SETTLEMENT#khipu:abcdefghijkl")
			if settlementErr != nil || settlement.Settlement == nil || settlement.Settlement.Amount != 20000 || settlement.Settlement.SettledGross != 0 {
				t.Fatalf("missing settlement receivable: %+v %v", settlement, settlementErr)
			}
		})
	}
}

func TestProviderConfirmationLateFundsRequireReview(t *testing.T) {
	s, db, v := confirmationFixture(t)
	a, err := s.GetAccount(context.Background(), "account")
	if err != nil {
		t.Fatal(err)
	}
	a.Active = false
	a.Installments[0].Cancelled = a.Installments[0].Outstanding()
	a.Version++
	item, err := attributevalue.MarshalMap(record{PK: "ACCOUNT#account", SK: "META", Version: a.Version, Account: &a})
	if err != nil {
		t.Fatal(err)
	}
	db.items[itemKey(item)] = item
	c, err := s.ConfirmProviderCheckout(context.Background(), v, "attempt", "abcdefghijkl", time.Now(), time.UTC)
	if err != nil || c.Event.Type != "PAYMENT_REQUIRES_REVIEW" || c.Account.UnappliedReceived != 20000 || c.Account.Installments[0].Paid != 0 {
		t.Fatalf("late payment lost or applied %+v %v", c, err)
	}
}

func TestProviderConfirmationRejectsUnverifiedFunds(t *testing.T) {
	for _, field := range []string{"amount", "currency", "status", "transaction", "provider error"} {
		t.Run(field, func(t *testing.T) {
			s, db, v := confirmationFixture(t)
			switch field {
			case "amount":
				v.result.Amount = "19999.01"
			case "currency":
				v.result.Currency = "USD"
			case "status":
				v.result.Status = "pending"
			case "transaction":
				v.result.TransactionID = "other"
			case "provider error":
				v.err = providers.ErrVerification
			}
			_, err := s.ConfirmProviderCheckout(context.Background(), v, "attempt", "abcdefghijkl", time.Now(), time.UTC)
			if !errors.Is(err, providers.ErrVerification) || db.commits != 2 {
				t.Fatalf("unverified money committed: %v", err)
			}
		})
	}
}
