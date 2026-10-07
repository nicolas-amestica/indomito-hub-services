package collection

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func groupDepositFixture(t *testing.T) (Service, *transactionDB, GroupDepositInput, []string) {
	t.Helper()
	db := &transactionDB{items: map[string]map[string]types.AttributeValue{}}
	service := Service{DB: db, Table: "payments"}
	trip := paymentOperationID("group-trip")
	ids := []string{paymentOperationID("group-a"), paymentOperationID("group-b"), paymentOperationID("group-c")}
	startup := domain.Startup{TripID: trip, ContractID: trip, ContractVersion: 2, Approved: true, PricePerPayer: 1000, DepositAgreed: 300, Participants: []domain.Participant{{ID: ids[0]}, {ID: ids[1]}, {ID: ids[2]}}, DueDates: []string{"2027-01-05"}}
	if err := service.PreparePlan(context.Background(), startup, "operator"); err != nil {
		t.Fatal(err)
	}
	db.commits, db.maxActions = 0, 0
	commandID := paymentOperationID("group-command")
	input := GroupDepositInput{ID: commandID, TripID: trip, Amount: 5, Reference: "bank:group-001", EffectiveDate: "2026-10-05", ReceiptEmail: "group@example.test", Audit: domain.Audit{CommandID: commandID, Actor: "operator", Reason: "Abono grupal confirmado", RecordedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}}
	return service, db, input, ids
}

func TestApplyGroupDepositPublishesExactAllocationOnce(t *testing.T) {
	service, db, input, ids := groupDepositFixture(t)
	state, err := service.ApplyGroupDeposit(context.Background(), input)
	if err != nil || state.Status != "APPLIED" || state.Applied != 3 || state.Expected != 3 {
		t.Fatalf("apply: %+v %v", state, err)
	}
	total := int64(0)
	amounts := map[int64]int{}
	for _, id := range ids {
		account, readErr := service.GetAccount(context.Background(), id)
		if readErr != nil {
			t.Fatal(readErr)
		}
		total += account.DepositReceived
		amounts[account.DepositReceived]++
		member, memberErr := service.read(context.Background(), "TRIP#"+input.TripID, "MEMBER#"+id)
		if memberErr != nil || member.Version != account.Version || member.Roster.Version != account.Version {
			t.Fatalf("roster version not synchronized: %+v %v", member, memberErr)
		}
	}
	if total != input.Amount || amounts[2] != 2 || amounts[1] != 1 {
		t.Fatalf("allocation total=%d amounts=%v", total, amounts)
	}
	projected := int64(0)
	for key, raw := range db.items {
		if !strings.HasPrefix(key, "TRIP#"+input.TripID+"/CASH#") {
			continue
		}
		var row record
		if err := attributevalue.UnmarshalMap(raw, &row); err != nil || row.Event == nil {
			t.Fatalf("invalid cash projection %s: %v", key, err)
		}
		for _, entry := range row.Event.Entries {
			if entry.Account == "BANK" {
				projected += entry.Amount
			}
		}
	}
	if projected != input.Amount {
		t.Fatalf("group deposit missing from cash: %d", projected)
	}
	if _, err = service.read(context.Background(), "REFERENCE#"+input.Reference, "META"); err != nil {
		t.Fatal("global bank reference not claimed")
	}
	receipt, receiptErr := service.read(context.Background(), "RECEIPT#"+input.ID, "META")
	if receiptErr != nil || receipt.Status != "PENDING_DOCUMENT" || receipt.ReceiptEmail != input.ReceiptEmail || receipt.Event.Type != "GROUP_DEPOSIT_RECEIVED" {
		t.Fatalf("group receipt missing: %+v %v", receipt, receiptErr)
	}
	commits := db.commits
	state, err = service.ApplyGroupDeposit(context.Background(), input)
	if err != nil || state.Status != "APPLIED" || db.commits != commits {
		t.Fatalf("replay changed money: %+v %v commits=%d", state, err, db.commits)
	}
}

func TestApplyGroupDepositResumesEveryBoundary(t *testing.T) {
	for offset := 1; offset <= 10; offset++ {
		t.Run(string(rune('A'+offset)), func(t *testing.T) {
			service, db, input, ids := groupDepositFixture(t)
			db.failOnCommit = offset
			_, firstErr := service.ApplyGroupDeposit(context.Background(), input)
			if firstErr == nil {
				t.Fatal("simulated interruption hidden")
			}
			db.failOnCommit = 0
			state, err := service.ApplyGroupDeposit(context.Background(), input)
			if err != nil || state.Status != "APPLIED" {
				t.Fatalf("resume: %+v %v (first %v)", state, err, firstErr)
			}
			total := int64(0)
			for _, id := range ids {
				account, readErr := service.GetAccount(context.Background(), id)
				if readErr != nil {
					t.Fatal(readErr)
				}
				total += account.DepositReceived
			}
			if total != input.Amount {
				t.Fatalf("money duplicated/lost: %d", total)
			}
		})
	}
}

func TestApplyGroupDepositConcurrentRetriesDoNotDuplicateMoney(t *testing.T) {
	service, _, input, ids := groupDepositFixture(t)
	errs := make(chan error, 8)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := service.ApplyGroupDeposit(context.Background(), input)
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
	if _, err := service.ApplyGroupDeposit(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	total := int64(0)
	for _, id := range ids {
		account, _ := service.GetAccount(context.Background(), id)
		total += account.DepositReceived
	}
	if total != input.Amount {
		t.Fatalf("concurrent total = %d", total)
	}
}
