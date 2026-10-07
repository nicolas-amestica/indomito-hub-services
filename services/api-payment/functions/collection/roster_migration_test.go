package collection

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func legacyRosterFixture(t *testing.T) (Service, *transactionDB, string, []RosterProjection, domain.Audit) {
	t.Helper()
	db := &transactionDB{items: map[string]map[string]types.AttributeValue{}}
	s := Service{DB: db, Table: "payments"}
	trip := paymentOperationID("legacy-roster")
	first, second := paymentOperationID("legacy-first"), paymentOperationID("legacy-second")
	startup := domain.Startup{TripID: trip, ContractID: trip, ContractVersion: 2, Approved: true, PricePerPayer: 100000, DueDates: []string{"2027-01-05"}, Participants: []domain.Participant{{ID: first, Name: "Ana Prueba", Document: "12345678-5"}, {ID: second, Name: "Pedro Prueba", Document: "16915292-6"}}}
	if err := s.PreparePlan(context.Background(), startup, "operator"); err != nil {
		t.Fatal(err)
	}
	db.mu.Lock()
	delete(db.items, "TRIP#"+trip+"/MEMBER#"+first)
	delete(db.items, "TRIP#"+trip+"/MEMBER#"+second)
	delete(db.items["TRIP#"+trip+"/META"], "rosterSchemaVersion")
	db.mu.Unlock()
	db.commits, db.maxActions = 0, 0
	profiles := []RosterProjection{{AccountID: first, ParticipantID: first, Name: "Ana Prueba", Document: "12345678-5"}, {AccountID: second, ParticipantID: second, Name: "Pedro Prueba", Document: "16915292-6"}}
	audit := domain.Audit{CommandID: paymentOperationID("roster-migration"), Actor: "operator", Reason: "Migracion controlada de nomina", RecordedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	return s, db, trip, profiles, audit
}

func TestBackfillRosterDoesNotChangeAccountsOrPlanFingerprint(t *testing.T) {
	s, db, trip, profiles, audit := legacyRosterFixture(t)
	beforeAccount, _ := json.Marshal(db.items["ACCOUNT#"+profiles[0].AccountID+"/META"])
	beforePlan, err := s.read(context.Background(), "TRIP#"+trip, "META")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListRoster(context.Background(), trip, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("legacy partial roster exposed: %v", err)
	}
	state, err := s.BackfillRoster(context.Background(), trip, profiles, audit)
	if err != nil || state.Status != "APPLIED" || state.Prepared != 2 || db.maxActions > 4 {
		t.Fatalf("migration failed: %+v actions=%d %v", state, db.maxActions, err)
	}
	afterAccount, _ := json.Marshal(db.items["ACCOUNT#"+profiles[0].AccountID+"/META"])
	afterPlan, _ := s.read(context.Background(), "TRIP#"+trip, "META")
	if string(beforeAccount) != string(afterAccount) || afterPlan.Fingerprint != beforePlan.Fingerprint || afterPlan.RosterSchemaVersion != 1 {
		t.Fatal("migration changed financial state or historical fingerprint")
	}
	page, err := s.ListRoster(context.Background(), trip, "")
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("migrated roster unavailable: %+v %v", page, err)
	}
	commits := db.commits
	if _, err = s.BackfillRoster(context.Background(), trip, profiles, audit); err != nil || db.commits != commits {
		t.Fatalf("replay wrote again: %v commits=%d", err, db.commits)
	}
}

func TestBackfillRosterResumesEveryTransactionBoundary(t *testing.T) {
	for fail := 1; fail <= 4; fail++ {
		t.Run(string(rune('0'+fail)), func(t *testing.T) {
			s, db, trip, profiles, audit := legacyRosterFixture(t)
			db.failOnCommit = fail
			if _, err := s.BackfillRoster(context.Background(), trip, profiles, audit); err == nil {
				t.Fatal("interruption hidden")
			}
			db.failOnCommit = 0
			state, err := s.BackfillRoster(context.Background(), trip, profiles, audit)
			if err != nil || state.Status != "APPLIED" || state.Prepared != 2 {
				t.Fatalf("migration did not resume: %+v %v", state, err)
			}
		})
	}
}

func TestBackfillRosterConcurrentRetriesDoNotDuplicateMembers(t *testing.T) {
	s, db, trip, profiles, audit := legacyRosterFixture(t)
	var wait sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := s.BackfillRoster(context.Background(), trip, profiles, audit)
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, domain.ErrConflict) {
			t.Fatal(err)
		}
	}
	state, err := s.BackfillRoster(context.Background(), trip, profiles, audit)
	if err != nil || state.Status != "APPLIED" {
		t.Fatalf("final retry failed: %+v %v", state, err)
	}
	page, _ := s.ListRoster(context.Background(), trip, "")
	if len(page.Items) != 2 || db.commits > 4 {
		t.Fatalf("members duplicated: %+v commits=%d", page, db.commits)
	}
}

func TestBackfillRosterRejectsIncompleteOrChangedIdentity(t *testing.T) {
	for name, mutate := range map[string]func([]RosterProjection) []RosterProjection{
		"missing member":      func(p []RosterProjection) []RosterProjection { return p[:1] },
		"changed participant": func(p []RosterProjection) []RosterProjection { p[0].ParticipantID = p[1].ParticipantID; return p },
		"empty identity":      func(p []RosterProjection) []RosterProjection { p[0].Name = ""; return p },
	} {
		t.Run(name, func(t *testing.T) {
			s, db, trip, profiles, audit := legacyRosterFixture(t)
			if _, err := s.BackfillRoster(context.Background(), trip, mutate(profiles), audit); err == nil || db.commits != 0 {
				t.Fatalf("invalid migration wrote data: %v", err)
			}
		})
	}
}
