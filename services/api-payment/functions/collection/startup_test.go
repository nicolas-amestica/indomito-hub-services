package collection

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestPrepareLargePlanUsesSmallTransactionsAndReplays(t *testing.T) {
	db := &transactionDB{items: map[string]map[string]types.AttributeValue{}}
	s := Service{DB: db, Table: "payments"}
	input := collection.Startup{TripID: "large", ContractID: "contract", ContractVersion: 2, Approved: true, PricePerPayer: 100001, DepositAgreed: 300000, DueDates: []string{"2027-01-05", "2027-02-05"}}
	for i := 0; i < 150; i++ {
		input.Participants = append(input.Participants, collection.Participant{ID: fmt.Sprintf("person-%03d", i)})
	}
	ctx := context.Background()
	if err := s.PreparePlan(ctx, input, "operator"); err != nil {
		t.Fatal(err)
	}
	plan, err := s.read(ctx, "TRIP#large", "META")
	if err != nil || plan.Status != "ACTIVE" || plan.Prepared != 150 {
		t.Fatalf("incomplete plan %+v %v", plan, err)
	}
	if _, err = s.GetAccount(ctx, "person-149"); err != nil {
		t.Fatal(err)
	}
	if db.maxActions > 3 {
		t.Fatalf("oversized startup transaction: %d", db.maxActions)
	}
	commits := db.commits
	if err = s.PreparePlan(ctx, input, "operator"); err != nil || db.commits != commits {
		t.Fatal("replay added writes")
	}
	input.PricePerPayer++
	if err = s.PreparePlan(ctx, input, "operator"); !errors.Is(err, ErrReplayMismatch) {
		t.Fatal("different plan silently overwrote accounts")
	}
}

func TestPreparePlanResumesAfterPartialFailureWithoutExposingAccounts(t *testing.T) {
	db := &transactionDB{items: map[string]map[string]types.AttributeValue{}, failOnCommit: 3}
	s := Service{DB: db, Table: "payments"}
	ctx := context.Background()
	input := collection.Startup{TripID: "trip", ContractID: "contract", ContractVersion: 2, Approved: true, PricePerPayer: 100000, Participants: []collection.Participant{{ID: "a"}, {ID: "b"}}, DueDates: []string{"2027-01-05"}}
	if err := s.PreparePlan(ctx, input, "operator"); err == nil {
		t.Fatal("expected interruption")
	}
	if _, err := s.GetAccount(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("exposed partially prepared plan")
	}
	plan, err := s.read(ctx, "TRIP#trip", "META")
	if err != nil || plan.Prepared != 1 {
		t.Fatal("prepared count inaccurate")
	}
	if err = s.publishPlan(ctx, "TRIP#trip", plan.Fingerprint); !errors.Is(err, collection.ErrConflict) {
		t.Fatal("incomplete plan published")
	}
	db.failOnCommit = 0
	if err = s.PreparePlan(ctx, input, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetAccount(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if db.commits != 4 {
		t.Fatalf("resuming counted account twice: %d", db.commits)
	}
}

func TestPreparePlanRejectsUnapprovedAndCancelledContext(t *testing.T) {
	db := &transactionDB{items: map[string]map[string]types.AttributeValue{}}
	s := Service{DB: db, Table: "payments"}
	if err := s.PreparePlan(context.Background(), collection.Startup{}, "operator"); !errors.Is(err, collection.ErrInvalid) {
		t.Fatal("invalid plan accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input := collection.Startup{TripID: "trip", ContractID: "contract", ContractVersion: 2, Approved: true, PricePerPayer: 100000, Participants: []collection.Participant{{ID: "a"}}, DueDates: []string{"2027-01-05"}}
	if err := s.PreparePlan(ctx, input, "operator"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context ignored: %v", err)
	}
	if db.commits != 0 {
		t.Fatal("wrote after cancellation")
	}
}

func TestAccountsRemainHiddenUntilPlanPublished(t *testing.T) {
	s, db := serviceFixture(t)
	db.items["TRIP#trip/META"]["status"] = &types.AttributeValueMemberS{Value: "PREPARING"}
	if _, err := s.GetAccount(context.Background(), "account"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial plan exposed: %v", err)
	}
}

func TestPreparePlanRecoversLostResponses(t *testing.T) {
	db := &transactionDB{items: map[string]map[string]types.AttributeValue{}, lostResponse: true}
	s := Service{DB: db, Table: "payments"}
	input := collection.Startup{TripID: "trip", ContractID: "contract", ContractVersion: 2, Approved: true, PricePerPayer: 100000, Participants: []collection.Participant{{ID: "a"}, {ID: "b"}}, DueDates: []string{"2027-01-05"}}
	if err := s.PreparePlan(context.Background(), input, "operator"); err != nil {
		t.Fatal(err)
	}
	if db.commits != 4 {
		t.Fatalf("duplicate writes: %d", db.commits)
	}
}
