package collection

import "testing"

func TestCashDistinguishesReceiptSettlementAndRefundApproval(t *testing.T) {
	a := accountFixture(t)
	c, attempt, err := OpenAttempt(a, audit("open"), "attempt", "payer@example.test")
	a = mustChange(t, c, err)
	c, err = ConfirmPayment(a, audit("received"), attempt, 20000, "khipu:1", "2026-09-29", false)
	a = mustChange(t, c, err)
	events := []Event{c.Event}
	p, err := ProjectCash(100000, "2026-09-01", "2026-09-30", "trip", events)
	if err != nil || p.Closing != 100000 {
		t.Fatal("receipt incorrectly counted as bank cash")
	}
	s, e, err := ReconcileSettlement(Settlement{PaymentReference: "khipu:1", TripID: "trip", Amount: 20000, Version: 1}, audit("settled"), 20000, 500, "bank:settlement", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, e)
	if _, _, err := ReconcileSettlement(s, audit("again"), 1, 0, "bank:duplicate", "2026-09-30"); err == nil {
		t.Fatal("over-settlement allowed")
	}
	c, err = Withdraw(a, audit("withdraw"), "annex")
	a = mustChange(t, c, err)
	events = append(events, c.Event)
	c, err = ApproveWithdrawalRefund(a, audit("approve"), 8000)
	a = mustChange(t, c, err)
	events = append(events, c.Event)
	p, err = ProjectCash(100000, "2026-09-01", "2026-09-30", "trip", events)
	if err != nil || p.Closing != 119500 {
		t.Fatalf("cash after approval=%+v err=%v", p, err)
	}
	if a.Position().RefundPayable != 16000 {
		t.Fatal("refund liability missing")
	}
	c, err = ConfirmRefund(a, audit("refunded"), 16000, "bank:refund", "2026-10-01")
	a = mustChange(t, c, err)
	events = append(events, c.Event)
	p, err = ProjectCash(119500, "2026-10-01", "2026-10-31", "trip", events)
	if err != nil || p.Closing != 103500 || a.Position().RefundPayable != 0 {
		t.Fatalf("cash after refund=%+v err=%v", p, err)
	}
}

func TestRemovingServicesDoesNotInventRecoveredCash(t *testing.T) {
	s, created, err := CreateSupplierCommitment("supplier", "trip", "Hotel Andes", "Alojamiento", 100000, audit("created supplier"))
	if err != nil || len(created.Entries) != 0 {
		t.Fatal("commitment was treated as cash")
	}
	s, e, err := PaySupplier(s, audit("paid supplier"), 60000, "bank:hotel", "2026-09-29")
	if err != nil {
		t.Fatal(err)
	}
	events := []Event{e}
	s, e, err = ReviseSupplier(s, audit("revise"), 50000, 10000, "annex")
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, e)
	p, err := ProjectCash(100000, "2026-09-01", "2026-09-30", "trip", events)
	if err != nil || p.Closing != 40000 || s.RefundReceived != 0 {
		t.Fatal("expected recovery booked as cash")
	}
	s, e, err = ReceiveSupplierRefund(s, audit("supplier returned"), 8000, "bank:return", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, e)
	p, err = ProjectCash(100000, "2026-09-01", "2026-09-30", "trip", events)
	if err != nil || p.Closing != 48000 || s.RefundAgreed-s.RefundReceived != 2000 {
		t.Fatal("partial supplier refund not tracked")
	}
}

func TestCashRejectsDuplicateOrUnbalancedEvents(t *testing.T) {
	e := Event{Audit: audit("one"), TripID: "trip", EffectiveDate: "2026-09-29", Entries: []Entry{{"BANK", 1000}, {"CUSTOMER_FUNDS", -1000}}}
	if _, err := ProjectCash(0, "2026-09-01", "2026-09-30", "", []Event{e, e}); err == nil {
		t.Fatal("duplicate event accepted")
	}
	e.Entries[1].Amount = -999
	if _, err := ProjectCash(0, "2026-09-01", "2026-09-30", "", []Event{e}); err == nil {
		t.Fatal("unbalanced event accepted")
	}
}
