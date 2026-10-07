package collection

import (
	"context"
	"errors"
	"testing"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestCloseRosterBlocksAnnexesButNotFinancialOperations(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	audit := domain.Audit{CommandID: paymentOperationID("close-roster"), Actor: "operator", Reason: "Nomina definitiva revisada", RecordedAt: time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)}
	if err := s.CloseRoster(context.Background(), input.TripID, audit); err != nil {
		t.Fatal(err)
	}
	plan, _ := s.read(context.Background(), "TRIP#"+input.TripID, "META")
	if plan.Status != "ACTIVE" || !plan.RosterClosed || plan.RosterClosure == nil || *plan.RosterClosure != audit {
		t.Fatalf("invalid closure: %+v", plan)
	}
	if _, err := s.PrepareAnnexDraft(context.Background(), input); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("closed roster accepted annex: %v", err)
	}
	account := input.Withdrawals[0].AccountID
	current, err := s.GetAccount(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	command := Command{ID: paymentOperationID("deposit-after-close"), AccountID: account, ExpectedVersion: current.Version, Reference: "bank:after-close", ReceiptID: paymentOperationID("receipt-after-close"), Payload: 1000}
	_, err = s.Apply(context.Background(), command, func(a domain.Account) (domain.Change, error) {
		return domain.RecordDeposit(a, domain.Audit{CommandID: command.ID, Actor: "operator", Reason: "Pago confirmado despues del cierre", RecordedAt: audit.RecordedAt.Add(time.Hour)}, 1000, command.Reference, "2026-10-05")
	})
	if err != nil {
		t.Fatalf("closure blocked legitimate payment: %v", err)
	}
	commits := db.commits
	if err = s.CloseRoster(context.Background(), input.TripID, audit); err != nil || db.commits != commits {
		t.Fatalf("closure replay wrote again: %v commits=%d", err, db.commits)
	}
}

func TestCloseRosterRejectsConcurrentAnnexAndDifferentReplay(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	audit := domain.Audit{CommandID: paymentOperationID("close-roster"), Actor: "operator", Reason: "Nomina definitiva revisada", RecordedAt: time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)}
	if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginAnnexApproval(context.Background(), input.TripID, input.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseRoster(context.Background(), input.TripID, audit); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("closure raced through annex: %v", err)
	}
	// Nuevo fixture para comprobar que una segunda identidad no puede atribuirse el cierre.
	s, db, input = annexDraftFixture(t)
	if err := s.CloseRoster(context.Background(), input.TripID, audit); err != nil {
		t.Fatal(err)
	}
	other := audit
	other.Actor = "another"
	if err := s.CloseRoster(context.Background(), input.TripID, other); !errors.Is(err, ErrReplayMismatch) || db.commits != 1 {
		t.Fatalf("different closure replay accepted: %v", err)
	}
}
