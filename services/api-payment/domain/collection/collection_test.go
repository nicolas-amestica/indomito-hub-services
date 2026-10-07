package collection

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func startupFixture() Startup {
	return Startup{TripID: "trip", ContractID: "contract", ContractVersion: 2, Approved: true, PricePerPayer: 110000, DepositAgreed: 20000, FreeCount: 1, Participants: []Participant{{ID: "a"}, {ID: "b"}, {ID: "c", Free: true}}, DueDates: []string{"2027-01-05", "2027-02-05", "2027-03-05", "2027-04-05", "2027-05-05"}, DaysBeforeDeparture: 10}
}
func audit(id string) Audit {
	return Audit{CommandID: id, Actor: "operator", Reason: "prueba", RecordedAt: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
}
func accountFixture(t *testing.T) Account {
	t.Helper()
	accounts, err := Start(startupFixture())
	if err != nil {
		t.Fatal(err)
	}
	return accounts[0]
}
func mustChange(t *testing.T, c Change, err error) Account {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Account.Validate(); err != nil {
		t.Fatal(err)
	}
	var balance int64
	for _, e := range c.Event.Entries {
		balance += e.Amount
	}
	if balance != 0 {
		t.Fatal("unbalanced journal")
	}
	return c.Account
}
func paidFixture(t *testing.T) Account {
	t.Helper()
	a := accountFixture(t)
	c, attempt, err := OpenAttempt(a, audit("open"), "attempt", "payer@example.test")
	a = mustChange(t, c, err)
	c, err = ConfirmPayment(a, audit("paid"), attempt, 20000, "khipu:1", "2026-09-29", false)
	return mustChange(t, c, err)
}

func TestStartupExactAllocationAndExplicitFree(t *testing.T) {
	for _, deposit := range []int64{0, 1, 19999, 20000, 219999, 220000} {
		t.Run(fmt.Sprint(deposit), func(t *testing.T) {
			s := startupFixture()
			s.DepositAgreed = deposit
			accounts, err := Start(s)
			if err != nil {
				t.Fatal(err)
			}
			var sum, allocated int64
			for _, a := range accounts {
				if a.DepositReceived != 0 {
					t.Fatal("pledge was counted as received")
				}
				allocated += a.DepositAgreed
				sum += a.DepositAgreed
				for _, i := range a.Installments {
					sum += i.Original
				}
				if a.ParticipantID == "c" && (!a.Free || len(a.Installments) != 0) {
					t.Fatal("free passenger billed")
				}
			}
			if sum != 220000 || allocated != deposit {
				t.Fatalf("total=%d deposit=%d", sum, allocated)
			}
			s.Participants[0], s.Participants[2] = s.Participants[2], s.Participants[0]
			again, err := Start(s)
			if err != nil || !reflect.DeepEqual(accounts, again) {
				t.Fatal("unstable remainder allocation")
			}
		})
	}
}

func TestStartupRejectsInvalidConfiguration(t *testing.T) {
	for name, mutate := range map[string]func(*Startup){
		"unapproved":            func(s *Startup) { s.Approved = false },
		"free mismatch":         func(s *Startup) { s.FreeCount = 0 },
		"duplicate participant": func(s *Startup) { s.Participants[1].ID = "a" },
		"missing due dates":     func(s *Startup) { s.DueDates = nil },
		"duplicate due date":    func(s *Startup) { s.DueDates[1] = s.DueDates[0] },
		"invalid date":          func(s *Startup) { s.DueDates[0] = "2027-02-30" },
		"deadline violated":     func(s *Startup) { s.DepartureDate = "2027-05-10" },
		"excess deposit":        func(s *Startup) { s.DepositAgreed = 220001 },
		"overflow":              func(s *Startup) { s.PricePerPayer = MaxAmount },
	} {
		t.Run(name, func(t *testing.T) {
			s := startupFixture()
			mutate(&s)
			if _, err := Start(s); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestFullInstallmentsSequentialAndExclusive(t *testing.T) {
	a := accountFixture(t)
	c, attempt, err := OpenAttempt(a, audit("open"), "attempt", "payer@example.test")
	a = mustChange(t, c, err)
	if _, _, err := OpenAttempt(a, audit("other"), "other", "payer@example.test"); !errors.Is(err, ErrConflict) {
		t.Fatal("parallel attempt allowed")
	}
	if _, err := Discount(a, audit("discount"), []string{"0001"}, 1000); !errors.Is(err, ErrConflict) {
		t.Fatal("discount changed open charge")
	}
	if _, err := ConfirmPayment(a, audit("manual"), attempt, 10000, "bank:1", "2026-09-29", true); err == nil {
		t.Fatal("manual partial payment allowed")
	}
	c, err = ConfirmPayment(a, audit("paid"), attempt, 20000, "khipu:1", "2026-09-29", false)
	a = mustChange(t, c, err)
	_, next, err := OpenAttempt(a, audit("next"), "next", "payer@example.test")
	if err != nil || next.InstallmentID != "0002" {
		t.Fatal("next installment not selected")
	}
}

func TestWithdrawalAndPartialRefundDoNotRewritePayments(t *testing.T) {
	a := paidFixture(t)
	before := a.Installments[0].Paid
	c, err := RecordDeposit(a, audit("deposit"), 10000, "bank:deposit", "2026-09-29")
	a = mustChange(t, c, err)
	c, err = Withdraw(a, audit("withdraw"), "annex")
	a = mustChange(t, c, err)
	c, err = ApproveWithdrawalRefund(a, audit("refund"), 8000)
	a = mustChange(t, c, err)
	if c.Event.Basis != 20000 || a.WithdrawalRefundApproved != 16000 || a.Refunded != 0 {
		t.Fatal("deposit included or approval counted as cash")
	}
	c, err = ConfirmRefund(a, audit("refund part"), 10000, "bank:refund1", "2026-09-30")
	a = mustChange(t, c, err)
	c, err = ConfirmRefund(a, audit("refund rest"), 6000, "bank:refund2", "2026-10-01")
	a = mustChange(t, c, err)
	if a.Installments[0].Paid != before || a.DepositReceived != 10000 || a.Refunded != 16000 {
		t.Fatal("history overwritten")
	}
	if _, err := ConfirmRefund(a, audit("excess"), 1, "bank:refund3", "2026-10-01"); err == nil {
		t.Fatal("over-refund allowed")
	}
	if _, err := ApproveWithdrawalRefund(a, audit("reduce"), 1000); err == nil {
		t.Fatal("approved liability silently reduced")
	}
}

func TestLateMismatchedAndDuplicateFundsRemainVisible(t *testing.T) {
	for _, kind := range []string{"late", "mismatch", "already applied"} {
		t.Run(kind, func(t *testing.T) {
			a := accountFixture(t)
			c, attempt, err := OpenAttempt(a, audit("open"), "attempt", "payer@example.test")
			a = mustChange(t, c, err)
			amount := int64(20000)
			switch kind {
			case "late":
				c, err = Withdraw(a, audit("withdraw"), "annex")
				a = mustChange(t, c, err)
			case "mismatch":
				amount = 19999
			case "already applied":
				c, err = ConfirmPayment(a, audit("first"), attempt, amount, "khipu:1", "2026-09-29", false)
				a = mustChange(t, c, err)
			}
			c, err = ConfirmPayment(a, audit("receipt"), attempt, amount, "khipu:2", "2026-09-29", false)
			a = mustChange(t, c, err)
			if a.UnappliedReceived != amount || c.Event.Type != "PAYMENT_REQUIRES_REVIEW" {
				t.Fatal("funds lost")
			}
			c, err = ApproveUnappliedRefund(a, audit("refund excess"), amount)
			a = mustChange(t, c, err)
			if a.UnappliedRefundApproved != amount {
				t.Fatal("withdrawal penalty applied to excess")
			}
		})
	}
}

func TestDiscountPreservesOriginalAndOtherAccounts(t *testing.T) {
	accounts, err := Start(startupFixture())
	if err != nil {
		t.Fatal(err)
	}
	before := accounts[0].Installments[0].Original
	c, err := Discount(accounts[0], audit("discount"), []string{"0001"}, 10000)
	a := mustChange(t, c, err)
	if a.Installments[0].Original != before || a.Installments[0].Outstanding() != 0 || accounts[0].Installments[0].Discount != 0 || accounts[1].Installments[0].Discount != 0 {
		t.Fatal("original input or other account mutated")
	}
	_, attempt, err := OpenAttempt(a, audit("open"), "attempt", "payer@example.test")
	if err != nil || attempt.InstallmentID != "0002" {
		t.Fatal("zero balance installment offered for payment")
	}
	if _, err := Discount(a, audit("unknown"), []string{"unknown"}, 1000); err == nil {
		t.Fatal("unknown installment accepted")
	}
}

func TestAllocateUnappliedFundsCoversOnlyWholeInstallmentsWithoutNewIncome(t *testing.T) {
	a := accountFixture(t)
	a.UnappliedReceived = 30000
	a.ReviewAttemptID = "attempt-review"
	a.Installments = []Installment{{ID: "0001", DueDate: "2027-01-05", Original: 20000}, {ID: "0002", DueDate: "2027-02-05", Original: 20000}}
	c, err := AllocateUnappliedFunds(a, audit("allocate-unapplied"), []string{"0001"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Account.Installments[0].Paid != 20000 || c.Account.Installments[1].Paid != 0 || c.Account.UnappliedReceived != 10000 || c.Account.ReviewAttemptID == "" {
		t.Fatalf("incorrect allocation: %+v", c.Account)
	}
	if c.Event.Amount != 20000 || len(c.Event.Entries) != 2 || c.Event.Entries[0].Account != "UNAPPLIED_FUNDS" || c.Event.Entries[0].Amount != 20000 || c.Event.Entries[1].Amount != -20000 {
		t.Fatalf("duplicated income: %+v", c.Event)
	}
	if _, err = AllocateUnappliedFunds(a, audit("partial"), []string{"0001", "0002"}); err == nil {
		t.Fatal("insufficient funds partially applied")
	}
	a.UnappliedRefundApproved = 1000
	if _, err = AllocateUnappliedFunds(a, audit("committed"), []string{"0001"}); err == nil {
		t.Fatal("refund-committed funds reallocated")
	}
}

func TestUnpaidResolutionRequiresFinalEvidence(t *testing.T) {
	a := accountFixture(t)
	c, attempt, err := OpenAttempt(a, audit("open"), "attempt", "payer@example.test")
	a = mustChange(t, c, err)
	if _, err := ResolveUnpaid(a, audit("expire"), attempt, "reference"); err == nil {
		t.Fatal("unresolved attempt released")
	}
	attempt.Status = "UNPAID_FINAL"
	c, err = ResolveUnpaid(a, audit("resolve"), attempt, "reference")
	a = mustChange(t, c, err)
	if a.OpenAttemptID != "" {
		t.Fatal("final unpaid attempt still locked")
	}
}

func FuzzStartupConservesCLP(f *testing.F) {
	f.Add(int64(100001), uint16(31), uint8(5))
	f.Fuzz(func(t *testing.T, total int64, people uint16, quantity uint8) {
		if total < 1 || total > MaxAmount || people < 1 || people > 1000 || quantity < 1 || quantity > 120 {
			return
		}
		var sum int64
		for p := 0; p < int(people); p++ {
			part := share(total, int(people), p)
			for q := 0; q < int(quantity); q++ {
				sum += share(part, int(quantity), q)
			}
		}
		if sum != total {
			t.Fatalf("allocated %d != %d", sum, total)
		}
	})
}
