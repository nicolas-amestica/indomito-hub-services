package collection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func annexDraftFixture(t *testing.T) (Service, *transactionDB, AnnexDraftInput) {
	t.Helper()
	db := &transactionDB{items: map[string]map[string]types.AttributeValue{}}
	s := Service{DB: db, Table: "payments"}
	trip, old, next, annex := paymentOperationID("trip"), paymentOperationID("old"), paymentOperationID("new"), paymentOperationID("annex")
	startup := domain.Startup{TripID: trip, ContractID: trip, ContractVersion: 2, Approved: true, PricePerPayer: 50000, DepositAgreed: 10000, Participants: []domain.Participant{{ID: old}}, DueDates: []string{"2027-01-05", "2027-02-05"}}
	if err := s.PreparePlan(context.Background(), startup, "operator"); err != nil {
		t.Fatal(err)
	}
	db.commits, db.maxActions = 0, 0
	input := AnnexDraftInput{ID: annex, TripID: trip, Audit: domain.Audit{CommandID: annex, Actor: "operator", Reason: "Reemplazo propuesto", RecordedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}, Withdrawals: []AnnexWithdrawal{{AccountID: old, ExpectedVersion: 1}}, Admissions: []domain.Admission{{AccountID: next, ParticipantID: next, TripID: trip, AnnexID: annex, DepositAgreed: 10000, Installments: []domain.AgreedInstallment{{ID: "01", DueDate: "2027-01-05", Amount: 20000}, {ID: "02", DueDate: "2027-02-05", Amount: 20000}}}}}
	return s, db, input
}

func TestAnnexDraftDoesNotChangeLiveAccountsOrMoney(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	ctx := context.Background()
	before, err := s.GetAccount(ctx, input.Withdrawals[0].AccountID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.PrepareAnnexDraft(ctx, input)
	if err != nil || state.Status != "DRAFT" || state.Prepared != 2 || state.Expected != 2 {
		t.Fatalf("draft: %+v %v", state, err)
	}
	after, err := s.GetAccount(ctx, before.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("draft changed original account: %+v %v", after, err)
	}
	if _, err := s.GetAccount(ctx, input.Admissions[0].AccountID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft published admission: %v", err)
	}
	proposal, err := s.read(ctx, "TRIP#"+input.TripID, "ANNEX#"+input.ID+"#PROPOSAL#"+before.ID)
	if err != nil || proposal.Change == nil || proposal.Change.Account.Active || proposal.Change.Event.Reference != input.ID || len(proposal.Change.Event.Entries) != 0 {
		t.Fatalf("withdrawal proposal: %+v %v", proposal, err)
	}
	if db.commits != 4 || db.maxActions > 3 {
		t.Fatalf("unexpected transactions: %d/%d", db.commits, db.maxActions)
	}
	rowsBefore, err := json.Marshal(db.items)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareAnnexDraft(ctx, input); err != nil {
		t.Fatal(err)
	}
	rowsAfter, err := json.Marshal(db.items)
	if err != nil || string(rowsBefore) != string(rowsAfter) || db.commits != 4 {
		t.Fatalf("replay mutated state: %v", err)
	}
	input.Admissions[0].DepositAgreed++
	if _, err := s.PrepareAnnexDraft(ctx, input); !errors.Is(err, ErrReplayMismatch) {
		t.Fatalf("changed draft reused id: %v", err)
	}
}

func TestAnnexDraftResumesEveryInterruptedStep(t *testing.T) {
	for _, fail := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			s, db, input := annexDraftFixture(t)
			db.failOnCommit = fail
			if _, err := s.PrepareAnnexDraft(context.Background(), input); err == nil {
				t.Fatal("interruption not reported")
			}
			if state, err := s.GetAnnexDraft(context.Background(), input.TripID, input.ID); err == nil && state.Status == "DRAFT" {
				t.Fatal("partial draft published")
			}
			a, err := s.GetAccount(context.Background(), input.Withdrawals[0].AccountID)
			if err != nil || !a.Active || a.Version != 1 {
				t.Fatalf("interrupted draft changed live account: %v", err)
			}
			db.failOnCommit = 0
			state, err := s.PrepareAnnexDraft(context.Background(), input)
			if err != nil || state.Status != "DRAFT" || state.Prepared != 2 || db.commits != 4 {
				t.Fatalf("resume: %+v commits=%d %v", state, db.commits, err)
			}
		})
	}
}

func TestAnnexDraftRecoversLostResponses(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	db.lostResponse = true
	state, err := s.PrepareAnnexDraft(context.Background(), input)
	if err != nil || state.Status != "DRAFT" || db.commits != 4 {
		t.Fatalf("lost response: %+v %d %v", state, db.commits, err)
	}
}

func TestAnnexDraftLargeBatchAndCanonicalReplay(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	input.Withdrawals, input.Admissions = nil, nil
	for i := 0; i < 150; i++ {
		id := paymentOperationID(fmt.Sprintf("new-%03d", i))
		input.Admissions = append(input.Admissions, domain.Admission{AccountID: id, ParticipantID: id, TripID: input.TripID, AnnexID: input.ID, Free: true})
	}
	state, err := s.PrepareAnnexDraft(context.Background(), input)
	if err != nil || state.Prepared != 150 || db.maxActions > 3 || db.commits != 152 {
		t.Fatalf("large draft: %+v commits=%d actions=%d %v", state, db.commits, db.maxActions, err)
	}
	for i, j := 0, len(input.Admissions)-1; i < j; i, j = i+1, j-1 {
		input.Admissions[i], input.Admissions[j] = input.Admissions[j], input.Admissions[i]
	}
	if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil || db.commits != 152 {
		t.Fatalf("order changed identity: %v", err)
	}
}

func TestAnnexDraftConcurrentRetriesNeverDoubleCount(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.PrepareAnnexDraft(context.Background(), input); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	state, err := s.GetAnnexDraft(context.Background(), input.TripID, input.ID)
	if err != nil || state.Prepared != 2 || db.commits != 4 {
		t.Fatalf("concurrency: %+v commits=%d %v", state, db.commits, err)
	}
}

func TestAnnexDraftRejectsConflictingProposals(t *testing.T) {
	tests := map[string]struct {
		mutate func(*AnnexDraftInput)
		want   error
	}{
		"stale withdrawal":     {func(i *AnnexDraftInput) { i.Withdrawals[0].ExpectedVersion++ }, domain.ErrConflict},
		"existing admission":   {func(i *AnnexDraftInput) { i.Admissions[0].AccountID = i.Withdrawals[0].AccountID; i.Withdrawals = nil }, domain.ErrConflict},
		"duplicate withdrawal": {func(i *AnnexDraftInput) { i.Withdrawals = append(i.Withdrawals, i.Withdrawals[0]) }, domain.ErrInvalid},
		"duplicate person": {func(i *AnnexDraftInput) {
			a := i.Admissions[0]
			a.AccountID = paymentOperationID("another")
			i.Admissions = append(i.Admissions, a)
		}, domain.ErrInvalid},
		"wrong trip": {func(i *AnnexDraftInput) { i.Admissions[0].TripID = paymentOperationID("another trip") }, domain.ErrInvalid},
		"invalid id": {func(i *AnnexDraftInput) { i.ID = "bad#PROPOSAL#key" }, domain.ErrInvalid},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			s, _, input := annexDraftFixture(t)
			test.mutate(&input)
			if _, err := s.PrepareAnnexDraft(context.Background(), input); !errors.Is(err, test.want) {
				t.Fatalf("conflict accepted: %v", err)
			}
		})
	}
}
