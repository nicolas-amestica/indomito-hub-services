package collection

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// transactionDB aplica las condiciones y todos los puts bajo el mismo bloqueo.
// No reemplaza la prueba de integración con DynamoDB: permite reproducir carreras de aplicación.
type transactionDB struct {
	mu           sync.Mutex
	items        map[string]map[string]types.AttributeValue
	lostResponse bool
	commits      int
	failOnCommit int
	maxActions   int
}

func itemKey(item map[string]types.AttributeValue) string {
	return item["pk"].(*types.AttributeValueMemberS).Value + "/" + item["sk"].(*types.AttributeValueMemberS).Value
}
func (d *transactionDB) GetItem(ctx context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return &dynamodb.GetItemOutput{Item: d.items[itemKey(in.Key)]}, nil
}
func (d *transactionDB) TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failOnCommit > 0 && d.commits+1 == d.failOnCommit {
		return nil, errors.New("simulated interruption before commit")
	}
	if len(in.TransactItems) > d.maxActions {
		d.maxActions = len(in.TransactItems)
	}
	for _, w := range in.TransactItems {
		if check := w.ConditionCheck; check != nil {
			old := d.items[itemKey(check.Key)]
			if *check.ConditionExpression != "#status = :active" && *check.ConditionExpression != "#status = :active AND (attribute_not_exists(rosterClosed) OR rosterClosed = :false)" && *check.ConditionExpression != "#status = :updating AND pendingAnnexId = :annex" && *check.ConditionExpression != "#status = :applying AND pendingGroupId = :command" && *check.ConditionExpression != "#status = :preparing AND pendingGroupId = :command" && *check.ConditionExpression != "#version = :version" {
				return nil, errors.New("unexpected condition check")
			}
			if *check.ConditionExpression == "#version = :version" {
				version, ok := old["version"].(*types.AttributeValueMemberN)
				if !ok || version.Value != check.ExpressionAttributeValues[":version"].(*types.AttributeValueMemberN).Value {
					return nil, &types.TransactionCanceledException{}
				}
				continue
			}
			status, ok := old["status"].(*types.AttributeValueMemberS)
			if !ok {
				return nil, &types.TransactionCanceledException{}
			}
			if *check.ConditionExpression == "#status = :active" || *check.ConditionExpression == "#status = :active AND (attribute_not_exists(rosterClosed) OR rosterClosed = :false)" {
				if status.Value != check.ExpressionAttributeValues[":active"].(*types.AttributeValueMemberS).Value {
					return nil, &types.TransactionCanceledException{}
				}
				if *check.ConditionExpression != "#status = :active" {
					if closed, exists := old["rosterClosed"].(*types.AttributeValueMemberBOOL); exists && closed.Value {
						return nil, &types.TransactionCanceledException{}
					}
				}
			} else if *check.ConditionExpression == "#status = :updating AND pendingAnnexId = :annex" {
				annex, annexOK := old["pendingAnnexId"].(*types.AttributeValueMemberS)
				if status.Value != check.ExpressionAttributeValues[":updating"].(*types.AttributeValueMemberS).Value || !annexOK || annex.Value != check.ExpressionAttributeValues[":annex"].(*types.AttributeValueMemberS).Value {
					return nil, &types.TransactionCanceledException{}
				}
			} else {
				command, commandOK := old["pendingGroupId"].(*types.AttributeValueMemberS)
				expected := check.ExpressionAttributeValues[":applying"]
				if expected == nil {
					expected = check.ExpressionAttributeValues[":preparing"]
				}
				if status.Value != expected.(*types.AttributeValueMemberS).Value || !commandOK || command.Value != check.ExpressionAttributeValues[":command"].(*types.AttributeValueMemberS).Value {
					return nil, &types.TransactionCanceledException{}
				}
			}
			continue
		}
		if deletion := w.Delete; deletion != nil {
			old := d.items[itemKey(deletion.Key)]
			if deletion.ConditionExpression == nil || *deletion.ConditionExpression != "#status = :pending" || old == nil {
				return nil, &types.TransactionCanceledException{}
			}
			status, ok := old["status"].(*types.AttributeValueMemberS)
			if !ok || status.Value != deletion.ExpressionAttributeValues[":pending"].(*types.AttributeValueMemberS).Value {
				return nil, &types.TransactionCanceledException{}
			}
			continue
		}
		old := d.items[itemKey(w.Put.Item)]
		switch *w.Put.ConditionExpression {
		case "#status = :active":
			if old == nil || old["status"].(*types.AttributeValueMemberS).Value != w.Put.ExpressionAttributeValues[":active"].(*types.AttributeValueMemberS).Value {
				return nil, &types.TransactionCanceledException{}
			}
		case "#status = :failed":
			if old == nil || old["status"].(*types.AttributeValueMemberS).Value != w.Put.ExpressionAttributeValues[":failed"].(*types.AttributeValueMemberS).Value {
				return nil, &types.TransactionCanceledException{}
			}
		case "#status = :pending":
			if old == nil || old["status"].(*types.AttributeValueMemberS).Value != w.Put.ExpressionAttributeValues[":pending"].(*types.AttributeValueMemberS).Value {
				return nil, &types.TransactionCanceledException{}
			}
		case "attribute_not_exists(pk) OR #status = :failed":
			if old != nil && old["status"].(*types.AttributeValueMemberS).Value != w.Put.ExpressionAttributeValues[":failed"].(*types.AttributeValueMemberS).Value {
				return nil, &types.TransactionCanceledException{}
			}
		case "attribute_not_exists(documentVersion) OR documentVersion = :v1":
			if old == nil {
				return nil, &types.TransactionCanceledException{}
			}
			if version, exists := old["documentVersion"].(*types.AttributeValueMemberN); exists && version.Value != "1" {
				return nil, &types.TransactionCanceledException{}
			}
		case "attribute_not_exists(documentVersion) OR documentVersion < :current":
			if old == nil {
				return nil, &types.TransactionCanceledException{}
			}
			if version, exists := old["documentVersion"].(*types.AttributeValueMemberN); exists && version.Value >= w.Put.ExpressionAttributeValues[":current"].(*types.AttributeValueMemberN).Value {
				return nil, &types.TransactionCanceledException{}
			}
		case "attribute_not_exists(pk) OR accountId = :previous":
			if old != nil && old["accountId"].(*types.AttributeValueMemberS).Value != w.Put.ExpressionAttributeValues[":previous"].(*types.AttributeValueMemberS).Value {
				return nil, &types.TransactionCanceledException{}
			}
		case "attribute_not_exists(pk) OR accountId = :accountId":
			if old != nil && (old["accountId"] == nil || old["accountId"].(*types.AttributeValueMemberS).Value != w.Put.ExpressionAttributeValues[":accountId"].(*types.AttributeValueMemberS).Value) {
				return nil, &types.TransactionCanceledException{}
			}
		case "attribute_not_exists(pk)":
			if old != nil {
				return nil, &types.TransactionCanceledException{}
			}
		case "#version = :previous":
			if old == nil || old["version"].(*types.AttributeValueMemberN).Value != w.Put.ExpressionAttributeValues[":previous"].(*types.AttributeValueMemberN).Value {
				return nil, &types.TransactionCanceledException{}
			}
		default:
			return nil, errors.New("unexpected condition")
		}
	}
	for _, w := range in.TransactItems {
		if w.Delete != nil {
			delete(d.items, itemKey(w.Delete.Key))
			continue
		}
		if w.Put == nil {
			continue
		}
		d.items[itemKey(w.Put.Item)] = w.Put.Item
	}
	d.commits++
	if d.lostResponse {
		return nil, errors.New("response lost after commit")
	}
	return &dynamodb.TransactWriteItemsOutput{}, nil
}
func serviceFixture(t *testing.T) (Service, *transactionDB) {
	t.Helper()
	a := domain.Account{ID: "account", TripID: "trip", ParticipantID: "person", Version: 1, Active: true, DepositAgreed: 10000, Installments: []domain.Installment{{ID: "0001", DueDate: "2027-01-05", Original: 20000}}}
	item, err := attributevalue.MarshalMap(record{PK: "ACCOUNT#account", SK: "META", Version: 1, Account: &a})
	if err != nil {
		t.Fatal(err)
	}
	d := &transactionDB{items: map[string]map[string]types.AttributeValue{itemKey(item): item}}
	plan, err := attributevalue.MarshalMap(record{PK: "TRIP#trip", SK: "META", Status: "ACTIVE"})
	if err != nil {
		t.Fatal(err)
	}
	d.items[itemKey(plan)] = plan
	return Service{DB: d, Table: "payments"}, d
}
func depositCommand(id, reference string) Command {
	return Command{ID: id, AccountID: "account", ExpectedVersion: 1, Reference: reference, ReceiptID: "receipt-" + id, Payload: struct{ Amount int64 }{10000}}
}
func depositTransition(id, reference string) func(domain.Account) (domain.Change, error) {
	return func(a domain.Account) (domain.Change, error) {
		return domain.RecordDeposit(a, domain.Audit{CommandID: id, Actor: "operator", Reason: "bank verified", RecordedAt: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}, 10000, reference, "2026-09-29")
	}
}

func TestReplayIsDurableAndRejectsDifferentInput(t *testing.T) {
	s, db := serviceFixture(t)
	ctx := context.Background()
	command := depositCommand("command", "bank:1")
	first, err := s.Apply(ctx, command, depositTransition(command.ID, command.Reference))
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Apply(ctx, command, func(domain.Account) (domain.Change, error) {
		t.Fatal("replayed transition ran")
		return domain.Change{}, nil
	})
	if err != nil || again.Account.Version != first.Account.Version || db.commits != 1 {
		t.Fatalf("replay: %v, commits=%d", err, db.commits)
	}
	command.Payload = "different"
	if _, err = s.Apply(ctx, command, depositTransition(command.ID, command.Reference)); !errors.Is(err, ErrReplayMismatch) {
		t.Fatalf("mismatched replay accepted: %v", err)
	}
	for _, pk := range []string{"RECEIPT#receipt-command/META", "JOB#2026-09-29/PENDING#receipt-command", "REFERENCE#bank:1/META", "ACCOUNT#account/EVENT#00000000000000000002"} {
		if db.items[pk] == nil {
			t.Fatalf("missing atomic side effect %s", pk)
		}
	}
	if db.items["TRIP#trip/CASH#2026-09-29#command"] == nil || db.items["CASH#2026-09/TRIP#trip#2026-09-29#command"] == nil {
		t.Fatal("bank deposit was not projected into cash")
	}
	if db.items["TRIP#trip/SETTLEMENT#bank:1"] != nil {
		t.Fatal("direct bank income incorrectly created a provider settlement")
	}
}

func TestConcurrentCommandsOnlyOneChangesBalance(t *testing.T) {
	s, db := serviceFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			command := depositCommand(id, "bank:"+id)
			_, err := s.Apply(ctx, command, depositTransition(id, command.Reference))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, domain.ErrConflict) {
			t.Fatal(err)
		}
	}
	a, err := s.GetAccount(ctx, "account")
	if err != nil || success != 1 || db.commits != 1 || a.DepositReceived != 10000 {
		t.Fatalf("success=%d commits=%d account=%+v err=%v", success, db.commits, a, err)
	}
}

func TestLostCommitResponseDoesNotDuplicateCash(t *testing.T) {
	s, db := serviceFixture(t)
	db.lostResponse = true
	command := depositCommand("one", "bank:one")
	change, err := s.Apply(context.Background(), command, depositTransition(command.ID, command.Reference))
	if err != nil || change.Account.DepositReceived != 10000 || db.commits != 1 {
		t.Fatalf("lost response not recovered: %v", err)
	}
}

func TestPlanChangedAfterReadRejectsEntireFinancialTransaction(t *testing.T) {
	for _, status := range []string{"UPDATING_ROSTER", "PREPARING", "CLOSED", "missing"} {
		t.Run(status, func(t *testing.T) {
			s, db := serviceFixture(t)
			command := depositCommand("blocked", "bank:blocked")
			_, err := s.Apply(context.Background(), command, func(a domain.Account) (domain.Change, error) {
				// Simula el commit de otra operación entre la lectura y la escritura.
				db.mu.Lock()
				if status == "missing" {
					delete(db.items, "TRIP#trip/META")
				} else {
					db.items["TRIP#trip/META"]["status"] = &types.AttributeValueMemberS{Value: status}
				}
				db.mu.Unlock()
				return depositTransition(command.ID, command.Reference)(a)
			})
			if !errors.Is(err, domain.ErrConflict) || db.commits != 0 {
				t.Fatalf("blocked plan committed: err=%v commits=%d", err, db.commits)
			}
			r, err := s.read(context.Background(), "ACCOUNT#account", "META")
			if err != nil || r.Version != 1 || r.Account.DepositReceived != 0 {
				t.Fatalf("account changed: %+v err=%v", r, err)
			}
			for k := range db.items {
				if k != "ACCOUNT#account/META" && k != "TRIP#trip/META" {
					t.Fatalf("partial financial side effect: %s", k)
				}
			}
		})
	}
}

func TestCommittedReplayRemainsAvailableWhilePlanIsLocked(t *testing.T) {
	s, db := serviceFixture(t)
	command := depositCommand("committed", "bank:committed")
	if _, err := s.Apply(context.Background(), command, depositTransition(command.ID, command.Reference)); err != nil {
		t.Fatal(err)
	}
	db.items["TRIP#trip/META"]["status"] = &types.AttributeValueMemberS{Value: "UPDATING_ROSTER"}
	change, err := s.Apply(context.Background(), command, func(domain.Account) (domain.Change, error) {
		t.Fatal("replay must not execute transition")
		return domain.Change{}, nil
	})
	if err != nil || change.Account.DepositReceived != 10000 || db.commits != 1 {
		t.Fatalf("committed replay failed: err=%v commits=%d", err, db.commits)
	}
}

func TestReferenceCannotBeReusedWithNewCommand(t *testing.T) {
	s, db := serviceFixture(t)
	command := depositCommand("one", "bank:one")
	ctx := context.Background()
	if _, err := s.Apply(ctx, command, depositTransition(command.ID, command.Reference)); err != nil {
		t.Fatal(err)
	}
	command = Command{ID: "two", AccountID: "account", ExpectedVersion: 2, Reference: "bank:one", ReceiptID: "another", Payload: 20000}
	_, err := s.Apply(ctx, command, func(a domain.Account) (domain.Change, error) {
		// Simula otro ingreso verificado con la misma referencia externa.
		return domain.ConfirmPayment(a, domain.Audit{CommandID: "two", Actor: "provider", Reason: "verified", RecordedAt: time.Now()}, domain.Attempt{ID: "late", AccountID: a.ID}, 20000, "bank:one", "2026-09-29", false)
	})
	if !errors.Is(err, ErrReferenceUsed) || db.commits != 1 {
		t.Fatalf("duplicate reference committed: %v", err)
	}
}

func TestCashCommandWithoutReferenceRejected(t *testing.T) {
	s, db := serviceFixture(t)
	command := depositCommand("one", "")
	_, err := s.Apply(context.Background(), command, depositTransition("one", "hidden-reference"))
	if !errors.Is(err, domain.ErrInvalid) || db.commits != 0 {
		t.Fatal("cash without deduplication accepted")
	}
}

func TestInvalidCommandCannotWrite(t *testing.T) {
	for _, name := range []string{"missing id", "invalid version", "unserializable payload", "wrong expected version"} {
		t.Run(name, func(t *testing.T) {
			s, db := serviceFixture(t)
			command := depositCommand("one", "bank:one")
			switch name {
			case "missing id":
				command.ID = ""
			case "invalid version":
				command.ExpectedVersion = 0
			case "unserializable payload":
				command.Payload = make(chan int)
			case "wrong expected version":
				command.ExpectedVersion = 2
			}
			if _, err := s.Apply(context.Background(), command, depositTransition(command.ID, command.Reference)); err == nil || db.commits != 0 {
				t.Fatalf("invalid command wrote state: %v", err)
			}
		})
	}
}
