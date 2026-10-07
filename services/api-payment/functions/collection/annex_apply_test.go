package collection

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestApplyAnnexPublishesAllEffectsWithoutMovingPayments(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	oldID, newID := input.Withdrawals[0].AccountID, input.Admissions[0].AccountID
	input.Replacements = []AnnexReplacement{{OutgoingAccountID: oldID, IncomingAccountID: newID}}
	input.LookupKeys = map[string]string{newID: "RUT#" + strings.Repeat("a", 64)}
	if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAnnex(context.Background(), input.TripID, input.ID); err != nil {
		t.Fatal(err)
	}
	plan, _ := s.read(context.Background(), "TRIP#"+input.TripID, "META")
	root, _ := s.read(context.Background(), "TRIP#"+input.TripID, "ANNEX#"+input.ID)
	oldAccount, err := s.GetAccount(context.Background(), oldID)
	if err != nil {
		t.Fatal(err)
	}
	newAccount, err := s.GetAccount(context.Background(), newID)
	if err != nil {
		t.Fatal(err)
	}
	link, err := s.read(context.Background(), "TRIP#"+input.TripID, input.LookupKeys[newID])
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "ACTIVE" || plan.PendingAnnexID != "" || root.Status != "APPLIED" || root.Applied != root.Expected || oldAccount.Active || oldAccount.DepositReceived != 0 || !newAccount.Active || newAccount.DepositReceived != 0 || newAccount.WithdrawalRefundApproved != 0 || link.AccountID != newID || db.maxActions > 7 {
		t.Fatalf("invalid publication plan=%+v root=%+v old=%+v new=%+v link=%+v", plan, root, oldAccount, newAccount, link)
	}
	commits := db.commits
	if err := s.ApplyAnnex(context.Background(), input.TripID, input.ID); err != nil || db.commits != commits {
		t.Fatalf("replay duplicated effects: %v commits=%d", err, db.commits)
	}
}

func TestApplyAnnexPersistsDistinctApprovalAndRejectsDifferentReplay(t *testing.T) {
	s, _, input := annexDraftFixture(t)
	if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	approval := domain.Audit{CommandID: paymentOperationID("approval"), Actor: "approver", Reason: "Nomina y montos revisados", RecordedAt: time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)}
	if err := s.ApplyAnnexWithApproval(context.Background(), input.TripID, input.ID, approval); err != nil {
		t.Fatal(err)
	}
	root, err := s.read(context.Background(), "TRIP#"+input.TripID, "ANNEX#"+input.ID)
	if err != nil || root.Approval == nil || *root.Approval != approval || root.Event == nil || root.Event.Actor == approval.Actor {
		t.Fatalf("approval audit was not kept separately: %+v %v", root, err)
	}
	other := approval
	other.Reason = "Un motivo distinto no es un replay"
	if err := s.ApplyAnnexWithApproval(context.Background(), input.TripID, input.ID, other); !errors.Is(err, ErrReplayMismatch) {
		t.Fatalf("different approval replay accepted: %v", err)
	}
}

func TestApplyAnnexResumesEveryTransactionBoundary(t *testing.T) {
	for _, offset := range []int{1, 2, 3, 4, 5} {
		t.Run(string(rune('0'+offset)), func(t *testing.T) {
			s, db, input := annexDraftFixture(t)
			if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			db.failOnCommit = db.commits + offset
			if err := s.ApplyAnnex(context.Background(), input.TripID, input.ID); err == nil {
				t.Fatal("interruption hidden")
			}
			db.failOnCommit = 0
			if err := s.ApplyAnnex(context.Background(), input.TripID, input.ID); err != nil {
				t.Fatal(err)
			}
			root, _ := s.read(context.Background(), "TRIP#"+input.TripID, "ANNEX#"+input.ID)
			if root.Status != "APPLIED" || root.Applied != 2 {
				t.Fatalf("not resumed: %+v", root)
			}
		})
	}
}

func TestApplyAnnexConcurrentRetriesDoNotDuplicateEffects(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.ApplyAnnex(context.Background(), input.TripID, input.ID) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, domain.ErrConflict) {
			t.Fatal(err)
		}
	}
	// Un competidor puede observar una transición intermedia y recibir conflicto;
	// consultar/reintentar recupera el resultado durable sin repetir efectos.
	if err := s.ApplyAnnex(context.Background(), input.TripID, input.ID); err != nil {
		t.Fatal(err)
	}
	root, _ := s.read(context.Background(), "TRIP#"+input.TripID, "ANNEX#"+input.ID)
	if root.Status != "APPLIED" || root.Applied != 2 {
		t.Fatalf("duplicated effects: %+v commits=%d", root, db.commits)
	}
}

func TestApplyAnnexRejectsLookupCollisionBeforeAnyEffect(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	lookup := "RUT#" + strings.Repeat("b", 64)
	input.LookupKeys = map[string]string{input.Admissions[0].AccountID: lookup}
	saveLookupRow(t, db, record{PK: "TRIP#" + input.TripID, SK: lookup, AccountID: paymentOperationID("existing"), TripID: input.TripID})
	if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAnnex(context.Background(), input.TripID, input.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("collision accepted: %v", err)
	}
	root, _ := s.read(context.Background(), "TRIP#"+input.TripID, "ANNEX#"+input.ID)
	oldAccount, err := s.GetAccount(context.Background(), input.Withdrawals[0].AccountID)
	if err != nil || root.Status != "REJECTED" || !oldAccount.Active || root.Applied != 0 {
		t.Fatalf("collision left partial effect: %+v %+v %v", root, oldAccount, err)
	}
}

func TestApplyAnnexReentryCreatesNewAccountAndMovesLookupConditionally(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	oldID, newID := input.Withdrawals[0].AccountID, input.Admissions[0].AccountID
	lookup := "RUT#" + strings.Repeat("c", 64)
	input.Replacements = []AnnexReplacement{{OutgoingAccountID: oldID, IncomingAccountID: newID}}
	input.LookupKeys = map[string]string{newID: lookup}
	saveLookupRow(t, db, record{PK: "TRIP#" + input.TripID, SK: lookup, AccountID: oldID, TripID: input.TripID})
	if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAnnex(context.Background(), input.TripID, input.ID); err != nil {
		t.Fatal(err)
	}
	oldAccount, oldErr := s.GetAccount(context.Background(), oldID)
	newAccount, newErr := s.GetAccount(context.Background(), newID)
	link, linkErr := s.read(context.Background(), "TRIP#"+input.TripID, lookup)
	oldMember, oldMemberErr := s.read(context.Background(), "TRIP#"+input.TripID, "MEMBER#"+oldID)
	newMember, newMemberErr := s.read(context.Background(), "TRIP#"+input.TripID, "MEMBER#"+newID)
	if oldErr != nil || newErr != nil || linkErr != nil || oldMemberErr != nil || newMemberErr != nil {
		t.Fatalf("missing reentry state: %v %v %v %v %v", oldErr, newErr, linkErr, oldMemberErr, newMemberErr)
	}
	if oldAccount.Active || !newAccount.Active || oldAccount.ID == newAccount.ID || link.AccountID != newID || oldMember.Roster.NextParticipation != newID || newMember.Roster.PreviousParticipation != oldID {
		t.Fatalf("invalid reentry old=%+v new=%+v link=%+v oldMember=%+v newMember=%+v", oldAccount, newAccount, link, oldMember.Roster, newMember.Roster)
	}
}

func TestApplyAnnexRejectsSameIdentityWithoutExplicitReplacement(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	lookup := "RUT#" + strings.Repeat("d", 64)
	input.LookupKeys = map[string]string{input.Admissions[0].AccountID: lookup}
	saveLookupRow(t, db, record{PK: "TRIP#" + input.TripID, SK: lookup, AccountID: input.Withdrawals[0].AccountID, TripID: input.TripID})
	if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAnnex(context.Background(), input.TripID, input.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ambiguous reentry accepted: %v", err)
	}
	root, _ := s.read(context.Background(), "TRIP#"+input.TripID, "ANNEX#"+input.ID)
	if root.Status != "REJECTED" || root.Applied != 0 {
		t.Fatalf("ambiguous reentry changed accounts: %+v", root)
	}
}
