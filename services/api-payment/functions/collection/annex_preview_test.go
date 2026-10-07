package collection

import (
	"context"
	"errors"
	"fmt"
	"testing"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestAnnexPreviewDetectsPaymentAfterDraftWithoutChangingProposal(t *testing.T) {
	s, _, input := annexDraftFixture(t)
	ctx := context.Background()
	if _, err := s.PrepareAnnexDraft(ctx, input); err != nil {
		t.Fatal(err)
	}
	page, err := s.PreviewAnnexDraft(ctx, input.TripID, input.ID, "")
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("preview: %+v %v", page, err)
	}
	for _, p := range page.Items {
		if p.Stale {
			t.Fatal("fresh proposal reported stale")
		}
		if p.Kind == "PARTICIPANT_WITHDRAWN" && (p.DebtRemoved != 50000 || p.DebtAdded != 0 || p.ExpectedVersion != 1) {
			t.Fatalf("withdrawal impact: %+v", p)
		}
		if p.Kind == "PARTICIPANT_ADMITTED" && (p.DebtAdded != 50000 || p.DebtRemoved != 0 || p.ExpectedVersion != 0) {
			t.Fatalf("admission impact: %+v", p)
		}
	}
	audit := input.Audit
	audit.CommandID = paymentOperationID("deposit-after-draft")
	command := Command{ID: audit.CommandID, AccountID: input.Withdrawals[0].AccountID, ExpectedVersion: 1, Reference: "bank:after-draft", Payload: 10000}
	if _, err := s.Apply(ctx, command, func(a domain.Account) (domain.Change, error) {
		return domain.RecordDeposit(a, audit, 10000, command.Reference, "2026-10-01")
	}); err != nil {
		t.Fatalf("draft blocked legitimate payment: %v", err)
	}
	page, err = s.PreviewAnnexDraft(ctx, input.TripID, input.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range page.Items {
		if p.Kind == "PARTICIPANT_WITHDRAWN" && (!p.Stale || p.CurrentVersion != 2 || p.ExpectedVersion != 1 || p.DebtRemoved != 50000) {
			t.Fatalf("stale proposal silently updated: %+v", p)
		}
		if p.Kind == "PARTICIPANT_ADMITTED" && p.Stale {
			t.Fatal("unrelated proposal changed")
		}
	}
	// Reanudar el borrador no debe cancelar el pago recibido ni recalcular la propuesta.
	if _, err := s.PrepareAnnexDraft(ctx, input); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetAccount(ctx, command.AccountID)
	if err != nil || a.DepositReceived != 10000 || !a.Active || a.Version != 2 {
		t.Fatalf("replay overwrote payment: %+v %v", a, err)
	}
}

func TestAnnexPreviewPaginatesOnlyItsOwnTripAndAnnex(t *testing.T) {
	s, _, input := annexDraftFixture(t)
	input.Withdrawals, input.Admissions = nil, nil
	for i := 0; i < 41; i++ {
		id := paymentOperationID(fmt.Sprintf("preview-%d", i))
		input.Admissions = append(input.Admissions, domain.Admission{AccountID: id, ParticipantID: id, TripID: input.TripID, AnnexID: input.ID, Free: true})
	}
	if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	cursor := ""
	seen := map[string]bool{}
	for _, count := range []int{20, 20, 1} {
		page, err := s.PreviewAnnexDraft(context.Background(), input.TripID, input.ID, cursor)
		if err != nil || len(page.Items) != count {
			t.Fatalf("pagination: %d %+v %v", count, page, err)
		}
		for _, row := range page.Items {
			if seen[row.AccountID] || !row.Free || row.DebtAdded != 0 {
				t.Fatalf("invalid or repeated row: %+v", row)
			}
			seen[row.AccountID] = true
		}
		cursor = page.NextCursor
	}
	if cursor != "" || len(seen) != 41 {
		t.Fatal("pagination incomplete")
	}
	if _, err := s.PreviewAnnexDraft(context.Background(), paymentOperationID("other-trip"), input.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign trip exposed: %v", err)
	}
	if _, err := s.PreviewAnnexDraft(context.Background(), input.TripID, input.ID, "ANNEX#foreign"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("arbitrary cursor accepted: %v", err)
	}
}

func TestAnnexPreviewRejectsIncompleteDraft(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	db.failOnCommit = 3
	if _, err := s.PrepareAnnexDraft(context.Background(), input); err == nil {
		t.Fatal("expected interruption")
	}
	if _, err := s.PreviewAnnexDraft(context.Background(), input.TripID, input.ID, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("partial draft exposed: %v", err)
	}
}

func TestAnnexDraftCancellationAndInactivePlan(t *testing.T) {
	for _, scenario := range []string{"cancelled", "inactive"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, input := annexDraftFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			} else {
				plan, err := s.read(ctx, "TRIP#"+input.TripID, "META")
				if err != nil {
					t.Fatal(err)
				}
				plan.Status = "UPDATING_ROSTER"
				saveLookupRow(t, db, plan)
			}
			if _, err := s.PrepareAnnexDraft(ctx, input); err == nil || db.commits != 0 {
				t.Fatalf("invalid state wrote draft: %v", err)
			}
		})
	}
}
