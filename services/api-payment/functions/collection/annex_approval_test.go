package collection

import (
	"context"
	"errors"
	"testing"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestBeginAnnexApprovalLocksOnlyAfterCompletePrevalidation(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	ctx := context.Background()
	if _, err := s.PrepareAnnexDraft(ctx, input); err != nil {
		t.Fatal(err)
	}
	before, err := s.read(ctx, "ACCOUNT#"+input.Withdrawals[0].AccountID, "META")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BeginAnnexApproval(ctx, input.TripID, input.ID); err != nil {
		t.Fatal(err)
	}
	plan, err := s.read(ctx, "TRIP#"+input.TripID, "META")
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.read(ctx, "TRIP#"+input.TripID, "ANNEX#"+input.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.read(ctx, "ACCOUNT#"+input.Withdrawals[0].AccountID, "META")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "UPDATING_ROSTER" || plan.PendingAnnexID != input.ID || root.Status != "APPLYING" || after.Version != before.Version || after.Account.Active != before.Account.Active {
		t.Fatalf("invalid prevalidation state: plan=%+v root=%+v account=%+v", plan, root, after)
	}
	commits := db.commits
	if err := s.BeginAnnexApproval(ctx, input.TripID, input.ID); err != nil || db.commits != commits {
		t.Fatalf("approval replay changed state: %v commits=%d", err, db.commits)
	}
}

func TestBeginAnnexApprovalRejectsEntireStaleDraftAndUnlocks(t *testing.T) {
	s, _, input := annexDraftFixture(t)
	ctx := context.Background()
	if _, err := s.PrepareAnnexDraft(ctx, input); err != nil {
		t.Fatal(err)
	}
	audit := input.Audit
	audit.CommandID = paymentOperationID("deposit-before-approval")
	command := Command{ID: audit.CommandID, AccountID: input.Withdrawals[0].AccountID, ExpectedVersion: 1, Reference: "bank:before-approval", Payload: 10000}
	if _, err := s.Apply(ctx, command, func(a domain.Account) (domain.Change, error) {
		return domain.RecordDeposit(a, audit, 10000, command.Reference, "2026-10-04")
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginAnnexApproval(ctx, input.TripID, input.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale draft accepted: %v", err)
	}
	plan, err := s.read(ctx, "TRIP#"+input.TripID, "META")
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.read(ctx, "TRIP#"+input.TripID, "ANNEX#"+input.ID)
	if err != nil {
		t.Fatal(err)
	}
	account, err := s.GetAccount(ctx, input.Withdrawals[0].AccountID)
	if err != nil || plan.Status != "ACTIVE" || plan.PendingAnnexID != "" || root.Status != "REJECTED" || account.DepositReceived != 10000 || !account.Active {
		t.Fatalf("rejection left partial state: plan=%+v root=%+v account=%+v err=%v", plan, root, account, err)
	}
	if _, err := s.GetAccount(ctx, input.Admissions[0].AccountID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale approval published admission: %v", err)
	}
}

func TestBeginAnnexApprovalResumesAfterValidationTransitionFailure(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	ctx := context.Background()
	if _, err := s.PrepareAnnexDraft(ctx, input); err != nil {
		t.Fatal(err)
	}
	db.failOnCommit = db.commits + 2
	if err := s.BeginAnnexApproval(ctx, input.TripID, input.ID); err == nil {
		t.Fatal("transition failure hidden")
	}
	plan, err := s.read(ctx, "TRIP#"+input.TripID, "META")
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.read(ctx, "TRIP#"+input.TripID, "ANNEX#"+input.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "UPDATING_ROSTER" || root.Status != "VALIDATING" {
		t.Fatalf("nonrecoverable state: %+v %+v", plan, root)
	}
	db.failOnCommit = 0
	if err := s.BeginAnnexApproval(ctx, input.TripID, input.ID); err != nil {
		t.Fatal(err)
	}
	root, err = s.read(ctx, "TRIP#"+input.TripID, "ANNEX#"+input.ID)
	if err != nil || root.Status != "APPLYING" {
		t.Fatalf("validation did not resume: %+v %v", root, err)
	}
}
