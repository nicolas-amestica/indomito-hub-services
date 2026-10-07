package collection

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func groupDiscountFixture(t *testing.T) (Service, *transactionDB, GroupDiscountInput, []string) {
	t.Helper()
	service, db, _, accounts := groupDepositFixture(t)
	input := GroupDiscountInput{
		ID:            paymentOperationID("group-discount"),
		TripID:        "",
		BasisPoints:   1000,
		CreationAudit: domain.Audit{CommandID: paymentOperationID("group-discount"), Actor: "operator", Reason: "Ayuda extraordinaria aprobada", RecordedAt: time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)},
	}
	account, err := service.GetAccount(context.Background(), accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	input.TripID = account.TripID
	return service, db, input, accounts
}

func TestGroupDiscountResumesEveryApprovalBoundary(t *testing.T) {
	for offset := 1; offset <= 8; offset++ {
		service, db, input, accounts := groupDiscountFixture(t)
		if _, err := service.CreateGroupDiscount(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		db.failOnCommit = db.commits + offset
		approval := domain.Audit{CommandID: paymentOperationID("approval-resume"), Actor: "supervisor", Reason: "Impacto completo revisado", RecordedAt: time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)}
		_, _ = service.ApproveGroupDiscount(context.Background(), input.TripID, input.ID, approval)
		db.failOnCommit = 0
		state, err := service.ApproveGroupDiscount(context.Background(), input.TripID, input.ID, approval)
		if err != nil || state.Status != "APPLIED" {
			t.Fatalf("offset %d: %+v %v", offset, state, err)
		}
		for _, id := range accounts {
			account, _ := service.GetAccount(context.Background(), id)
			if account.Installments[0].Discount != 90 {
				t.Fatalf("offset %d duplicated discount", offset)
			}
		}
	}
}

func TestGroupDiscountConcurrentApprovalDoesNotDuplicate(t *testing.T) {
	service, _, input, accounts := groupDiscountFixture(t)
	if _, err := service.CreateGroupDiscount(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	approval := domain.Audit{CommandID: paymentOperationID("approval-concurrent"), Actor: "supervisor", Reason: "Beneficiarios revisados", RecordedAt: time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)}
	var group sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := service.ApproveGroupDiscount(context.Background(), input.TripID, input.ID, approval)
			errs <- err
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, domain.ErrConflict) {
			t.Fatal(err)
		}
	}
	state, err := service.ApproveGroupDiscount(context.Background(), input.TripID, input.ID, approval)
	if err != nil || state.Status != "APPLIED" {
		t.Fatal(err)
	}
	for _, id := range accounts {
		account, _ := service.GetAccount(context.Background(), id)
		if account.Installments[0].Discount != 90 {
			t.Fatal("concurrent duplicate")
		}
	}
}

func TestGroupDiscountDraftApprovalAndReplay(t *testing.T) {
	service, db, input, accounts := groupDiscountFixture(t)
	state, err := service.CreateGroupDiscount(context.Background(), input)
	if err != nil || state.Status != "DRAFT" || state.Expected != 3 {
		t.Fatalf("draft: %+v %v", state, err)
	}
	for _, id := range accounts {
		account, _ := service.GetAccount(context.Background(), id)
		if account.Installments[0].Discount != 0 {
			t.Fatal("draft changed debt")
		}
	}
	approval := domain.Audit{CommandID: paymentOperationID("approve-group-discount"), Actor: "supervisor", Reason: "Beneficiarios e impacto revisados", RecordedAt: time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)}
	state, err = service.ApproveGroupDiscount(context.Background(), input.TripID, input.ID, approval)
	if err != nil || state.Status != "APPLIED" || state.Applied != 3 || state.Amount != 270 {
		t.Fatalf("approval: %+v %v", state, err)
	}
	commits := db.commits
	state, err = service.ApproveGroupDiscount(context.Background(), input.TripID, input.ID, approval)
	if err != nil || state.Status != "APPLIED" || db.commits != commits {
		t.Fatal("approval replay changed state")
	}
	for _, id := range accounts {
		account, _ := service.GetAccount(context.Background(), id)
		if account.Installments[0].Discount != 90 {
			t.Fatalf("wrong discount for %s: %+v", id, account.Installments)
		}
	}
}

func TestGroupDiscountFreezesSelectedBeneficiariesAndRejectsOpenAttempt(t *testing.T) {
	service, _, input, accounts := groupDiscountFixture(t)
	input.AccountIDs = []string{accounts[1], accounts[0]}
	state, err := service.CreateGroupDiscount(context.Background(), input)
	if err != nil || state.Expected != 2 {
		t.Fatalf("selected: %+v %v", state, err)
	}
	third, _ := service.GetAccount(context.Background(), accounts[2])
	if third.Installments[0].Discount != 0 {
		t.Fatal("unselected account changed")
	}
	service2, db2, input2, accounts2 := groupDiscountFixture(t)
	row, _ := service2.read(context.Background(), "ACCOUNT#"+accounts2[0], "META")
	row.Account.OpenAttemptID = paymentOperationID("open-attempt")
	saveLookupRow(t, db2, row)
	if _, err = service2.CreateGroupDiscount(context.Background(), input2); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("open attempt accepted: %v", err)
	}
}

func TestGroupDiscountRejectsStaleDraftBeforeAnyEffect(t *testing.T) {
	service, _, input, accounts := groupDiscountFixture(t)
	if _, err := service.CreateGroupDiscount(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	current, _ := service.GetAccount(context.Background(), accounts[0])
	audit := domain.Audit{CommandID: paymentOperationID("intervening"), Actor: "operator", Reason: "Cambio posterior al borrador", RecordedAt: time.Date(2026, 10, 5, 14, 30, 0, 0, time.UTC)}
	change, err := domain.Discount(current, audit, []string{current.Installments[0].ID}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), Command{ID: audit.CommandID, AccountID: current.ID, ExpectedVersion: current.Version, Payload: "intervening"}, func(domain.Account) (domain.Change, error) { return change, nil }); err != nil {
		t.Fatal(err)
	}
	approval := domain.Audit{CommandID: paymentOperationID("approval-stale"), Actor: "supervisor", Reason: "Impacto revisado", RecordedAt: time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)}
	if _, err = service.ApproveGroupDiscount(context.Background(), input.TripID, input.ID, approval); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale draft accepted: %v", err)
	}
	for _, id := range accounts[1:] {
		account, _ := service.GetAccount(context.Background(), id)
		if account.Installments[0].Discount != 0 {
			t.Fatal("partial stale discount")
		}
	}
	plan, _ := service.read(context.Background(), "TRIP#"+input.TripID, "META")
	if plan.Status != "ACTIVE" || plan.PendingGroupID != "" {
		t.Fatal("rejected draft left plan locked")
	}
}
