package collection

import (
	"errors"
	"testing"
)

func TestPaymentReviewBlocksNewAttemptsWithoutLosingLateFunds(t *testing.T) {
	a := accountFixture(t)
	c, attempt, err := OpenAttempt(a, audit("open-review"), "attempt-review", "payer@example.test")
	a = mustChange(t, c, err)
	c, err = ConfirmPayment(a, audit("mismatch"), attempt, attempt.Amount+1, "khipu:first", "2026-09-29", false)
	a = mustChange(t, c, err)
	if a.OpenAttemptID != "" || a.ReviewAttemptID != attempt.ID || !a.RequiresPaymentReview() {
		t.Fatal("review not durable")
	}
	if _, _, err := OpenAttempt(a, audit("retry"), "new", "payer@example.test"); !errors.Is(err, ErrConflict) {
		t.Fatal("allowed another charge")
	}
	other := attempt
	other.ID = "late-other"
	c, err = ConfirmPayment(a, audit("late"), other, 100, "khipu:second", "2026-09-29", false)
	a = mustChange(t, c, err)
	if a.UnappliedReceived != attempt.Amount+101 || a.ReviewAttemptID != attempt.ID {
		t.Fatal("lost funds or first review reference")
	}
}

func TestPaymentReviewProtectsLegacyAndApprovedRefund(t *testing.T) {
	for _, approved := range []int64{0, 100} {
		a := accountFixture(t)
		a.UnappliedReceived = 100
		a.UnappliedRefundApproved = approved
		if _, _, err := OpenAttempt(a, audit("new"), "new", "payer@example.test"); !errors.Is(err, ErrConflict) {
			t.Fatal("legacy funds or refund approval unlocked checkout")
		}
	}
}
