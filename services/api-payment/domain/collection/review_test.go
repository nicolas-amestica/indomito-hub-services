package collection

import "testing"

func TestReviewResolutionRequiresActualFullRefundAndPreservesHistory(t *testing.T) {
	a := accountFixture(t)
	a.UnappliedReceived = 100
	a.ReviewAttemptID = "review"
	if _, err := ResolveRefundedReview(a, audit("no-approval")); err == nil {
		t.Fatal("unapproved resolution")
	}
	c, err := ApproveUnappliedRefund(a, audit("approve"), 100)
	a = mustChange(t, c, err)
	if _, err := ResolveRefundedReview(a, audit("no-cash")); err == nil {
		t.Fatal("approval treated as refund")
	}
	c, err = ConfirmRefund(a, audit("partial"), 40, "bank:partial", "2026-10-01")
	a = mustChange(t, c, err)
	if _, err := ResolveRefundedReview(a, audit("partial-close")); err == nil {
		t.Fatal("partial refund unlocked")
	}
	c, err = ConfirmRefund(a, audit("rest"), 60, "bank:rest", "2026-10-01")
	a = mustChange(t, c, err)
	c, err = ResolveRefundedReview(a, audit("close"))
	a = mustChange(t, c, err)
	if a.RequiresPaymentReview() || a.UnappliedReceived != 100 || a.Refunded != 100 || len(c.Event.Entries) != 0 {
		t.Fatal("lost history or fabricated cash")
	}
	if _, _, err := OpenAttempt(a, audit("next"), "next", "payer@example.test"); err != nil {
		t.Fatal(err)
	}
	a.UnappliedReceived++
	if !a.RequiresPaymentReview() {
		t.Fatal("new funds did not reopen review")
	}
}

func TestManualInstallmentIsFullAndBlockedByOpenAttempt(t *testing.T) {
	a := accountFixture(t)
	c, err := RecordManualInstallment(a, audit("manual"), "bank:manual", "2026-10-01")
	next := mustChange(t, c, err)
	if next.Installments[0].Outstanding() != 0 || c.Event.Amount != a.Installments[0].Outstanding() || c.Event.Entries[0].Account != "BANK" {
		t.Fatal("incorrect manual payment")
	}
	a.OpenAttemptID = "open"
	if _, err := RecordManualInstallment(a, audit("conflict"), "bank:other", "2026-10-01"); err == nil {
		t.Fatal("manual raced Khipu")
	}
}
