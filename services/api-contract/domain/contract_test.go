package domain

import (
	"testing"
	"time"
)

func TestNewItemRequiresPassengers(t *testing.T) {
	_, err := NewItem("01ARZ3NDEKTSV4RRFFQ69G5FAV", "", "2026-09", Content{}, time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected passenger validation error")
	}
}
func TestApprovedAndCancelledAreTerminal(t *testing.T) {
	for _, s := range []Status{StatusApproved, StatusCancelled} {
		if CanTransition(s, StatusDraft) {
			t.Fatalf("%s must be terminal", s)
		}
	}
}
func TestReviewTransitions(t *testing.T) {
	cases := []struct {
		from, to Status
		want     bool
	}{{StatusDraft, StatusPendingApproval, true}, {StatusDraft, StatusApproved, false}, {StatusPendingApproval, StatusApproved, true}, {StatusRejected, StatusDraft, true}}
	for _, tc := range cases {
		if got := CanTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("%s -> %s = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}
