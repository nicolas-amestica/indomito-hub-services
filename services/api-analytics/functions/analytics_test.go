package functions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	budget "ind-hub-api-gox-sls-pri-gh/services/api-analytics/domain/analytics"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"sync"
	"testing"
	"time"
)

const tid = "01J00000000000000000000000"

type memoryDB struct {
	mu     sync.Mutex
	items  map[string]map[string]types.AttributeValue
	query  func(*dynamodb.QueryInput) (*dynamodb.QueryOutput, error)
	writes int
}

func itemKey(table string, item map[string]types.AttributeValue) string {
	return table + "/" + item["pk"].(*types.AttributeValueMemberS).Value + "/" + item["sk"].(*types.AttributeValueMemberS).Value
}
func (db *memoryDB) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return &dynamodb.GetItemOutput{Item: db.items[itemKey(*in.TableName, in.Key)]}, nil
}
func (db *memoryDB) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	if db.query != nil {
		return db.query(in)
	}
	return &dynamodb.QueryOutput{}, nil
}
func (db *memoryDB) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, write := range in.TransactItems {
		p := write.Put
		old := db.items[itemKey(*p.TableName, p.Item)]
		if aws.ToString(p.ConditionExpression) == "attribute_not_exists(pk)" && old != nil {
			return nil, &types.TransactionCanceledException{}
		}
		if expected, ok := p.ExpressionAttributeValues[":v"].(*types.AttributeValueMemberN); ok {
			v, _ := old["version"].(*types.AttributeValueMemberN)
			if v == nil || v.Value != expected.Value {
				return nil, &types.TransactionCanceledException{}
			}
		}
	}
	for _, write := range in.TransactItems {
		p := write.Put
		db.items[itemKey(*p.TableName, p.Item)] = p.Item
	}
	db.writes++
	return &dynamodb.TransactWriteItemsOutput{}, nil
}
func testBudget() budget.Budget {
	return budget.Budget{Name: "Viaje escolar", TotalPassengers: 32, FreePassengers: 2, Days: 5, Nights: 3, TargetMode: "amount", Source: budget.Source{Kind: "manual"}, Taxes: budget.Taxes{VatRate: 19, Source: "Configuración", CostBasis: "net"}, Categories: []budget.Category{{ID: tid, Name: "Hotel"}}, Lines: []budget.Line{}}
}
func TestPersistenceIsVersionedIdempotentAndNeverWritesPayments(t *testing.T) {
	db := &memoryDB{items: map[string]map[string]types.AttributeValue{}}
	s := Service{DB: db, Table: "programs", PaymentsTable: "payments"}
	b := testBudget()
	command := budget.Command{CommandID: tid, Action: "SAVE", Budget: &b, Reason: "Primer borrador"}
	first, err := s.SaveAnalytics(context.Background(), tid, command, "actor", time.Now())
	if err != nil || first.Version != 1 {
		t.Fatal(err)
	}
	replay, err := s.SaveAnalytics(context.Background(), tid, command, "actor", time.Now())
	if err != nil || replay.Version != 1 || db.writes != 1 {
		t.Fatal("replay no fue idempotente", err)
	}
	changed := command
	changed.Reason = "Otra intención"
	if _, err = s.SaveAnalytics(context.Background(), tid, changed, "actor", time.Now()); !errors.Is(err, ErrReplayMismatch) {
		t.Fatal("se aceptó reutilización", err)
	}
	stale := command
	stale.CommandID = "01J00000000000000000000001"
	if _, err = s.SaveAnalytics(context.Background(), tid, stale, "actor", time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("se aceptó versión obsoleta", err)
	}
	for key := range db.items {
		if len(key) >= 8 && key[:8] == "payments" {
			t.Fatal("se escribió en pagos")
		}
	}
	if len(db.items) != 3 {
		t.Fatal("faltan estado, revisión o recibo", len(db.items))
	}
}
func TestConcurrentWritersHaveOneWinner(t *testing.T) {
	db := &memoryDB{items: map[string]map[string]types.AttributeValue{}}
	s := Service{DB: db, Table: "programs"}
	b := testBudget()
	var group sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func(n int) {
			defer group.Done()
			_, err := s.SaveAnalytics(context.Background(), tid, budget.Command{CommandID: fmt.Sprintf("01J0000000000000000000000%d", n), Action: "SAVE", Budget: &b, Reason: "Escritura concurrente"}, "actor", time.Now())
			results <- err
		}(i)
	}
	group.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, domain.ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("escritores exitosos", wins)
	}
}
func TestContractQuantitiesAndMissingFinanceAreExplicit(t *testing.T) {
	db := &memoryDB{items: map[string]map[string]types.AttributeValue{}}
	raw := map[string]interface{}{"pk": "CONTRACT#" + tid, "sk": "METADATA", "id": tid, "status": "APPROVED", "version": int64(1), "content": map[string]interface{}{"payments": map[string]interface{}{"totalPassengers": 30, "freePassengers": 2}, "passengers": make([]struct{}, 32)}}
	item, err := attributevalue.MarshalMap(raw)
	if err != nil {
		t.Fatal(err)
	}
	db.items[itemKey("programs", item)] = item
	app := AdminApp{Contracts: Service{DB: db, Table: "programs"}, Accounts: Service{DB: db, Table: "programs", PaymentsTable: "payments"}}
	trip, err := app.analyticsTrip(context.Background(), tid)
	if err != nil || !trip.QuantityConsistent || trip.Content.Payments.TotalPassengers != 30 || trip.RosterCount != 32 {
		t.Fatal("cantidades incorrectas", err)
	}
	finance, issue, err := app.Accounts.analyticsFinance(context.Background(), tid, "2026-01-01", "2026-11-18")
	if err != nil || finance != nil || issue == "" {
		t.Fatal("datos ausentes tratados como cero")
	}
}
func TestRevisionPaginationAndUnauthorizedIdentity(t *testing.T) {
	db := &memoryDB{items: map[string]map[string]types.AttributeValue{}}
	calls := 0
	db.query = func(in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
		calls++
		state := budget.State{Version: int64(calls), Audit: budget.Audit{Actor: "actor"}}
		raw, _ := json.Marshal(state)
		item, _ := attributevalue.MarshalMap(analyticsRecord{PK: "TRIP#" + tid, SK: fmt.Sprint(calls), Data: string(raw)})
		out := &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{item}}
		if calls == 1 {
			out.LastEvaluatedKey = key("TRIP#"+tid, "next")
		}
		return out, nil
	}
	s := Service{DB: db, Table: "programs"}
	revisions, err := s.analyticsRevisions(context.Background(), tid)
	if err != nil || len(revisions) != 2 || calls != 2 {
		t.Fatal("paginación incompleta", err)
	}
	response, err := AnalyticsHandler(context.Background(), events.APIGatewayV2HTTPRequest{})
	if err != nil || response.StatusCode != 401 {
		t.Fatal("solicitud sin identidad aceptada", response.StatusCode, err)
	}
}

func TestFinancialReadReusesCurrentAccountsAndReconcilesReceiptHistory(t *testing.T) {
	ctx := context.Background()
	db := &memoryDB{items: map[string]map[string]types.AttributeValue{}}
	put := func(table, pk, sk string, value interface{}) {
		raw, err := attributevalue.MarshalMap(value)
		if err != nil {
			t.Fatal(err)
		}
		raw["pk"] = &types.AttributeValueMemberS{Value: pk}
		raw["sk"] = &types.AttributeValueMemberS{Value: sk}
		db.items[itemKey(table, raw)] = raw
	}
	account := domain.Account{ID: tid, TripID: tid, ParticipantID: tid, Version: 1, Active: true, Installments: []domain.Installment{{ID: tid, DueDate: "2026-10-01", Original: 500, Paid: 200}}}
	put("payments", "TRIP#"+tid, "SETUP", record{})
	put("payments", "TRIP#"+tid, "META", record{Version: 1, Status: "ACTIVE", RosterSchemaVersion: 1})
	put("payments", "ACCOUNT#"+tid, "META", record{Version: 1, Account: &account})
	member, err := attributevalue.MarshalMap(record{Roster: &Roster{AccountID: tid}})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := attributevalue.MarshalMap(record{Event: &domain.Event{Type: "PAYMENT_RECEIVED", TripID: tid, AccountID: tid, InstallmentID: tid, Amount: 200, EffectiveDate: "2026-10-01"}})
	if err != nil {
		t.Fatal(err)
	}
	cash, err := attributevalue.MarshalMap(record{Event: &domain.Event{Audit: domain.Audit{CommandID: tid, Actor: "test", Reason: "Movimiento ficticio para prueba", RecordedAt: time.Now()}, Type: "PAYMENT_SETTLED", TripID: tid, Amount: 200, EffectiveDate: "2026-10-01", Entries: []domain.Entry{{Account: "BANK", Amount: 190}, {Account: "PAYMENT_FEES", Amount: 10}, {Account: "PROVIDER_RECEIVABLE", Amount: -200}}}})
	if err != nil {
		t.Fatal(err)
	}
	settlement, err := attributevalue.MarshalMap(record{Settlement: &domain.Settlement{TripID: tid, Amount: 200, SettledGross: 200, ActualFees: 10, Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	db.query = func(input *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
		if aws.ToString(input.TableName) != "payments" {
			t.Fatal("se consultó otra tabla", aws.ToString(input.TableName))
		}
		prefix, _ := input.ExpressionAttributeValues[":prefix"].(*types.AttributeValueMemberS)
		var items []map[string]types.AttributeValue
		if prefix == nil {
			items = []map[string]types.AttributeValue{cash}
		} else {
			switch prefix.Value {
			case "MEMBER#":
				items = []map[string]types.AttributeValue{member}
			case "EVENT#":
				items = []map[string]types.AttributeValue{journal}
			case "SETTLEMENT#":
				items = []map[string]types.AttributeValue{settlement}
			}
		}
		return &dynamodb.QueryOutput{Items: items}, nil
	}
	s := Service{DB: db, Table: "programs", PaymentsTable: "payments"}
	view, issue, err := s.analyticsFinance(ctx, tid, "2026-01-01", "2026-11-18")
	if err != nil {
		t.Fatal(err)
	}
	if issue != "" || view.ContractedSale != 500 || view.Position.AppliedReceipts != 200 || view.Position.Receivable != 300 || view.Closing != 190 || view.ActualFees != 10 || !view.HistoryComplete || view.Aging[1] != 300 {
		t.Fatalf("proyección incorrecta: %+v", view)
	}
	// Una cuota pagada sin eventos suficientes nunca habilita un histórico inventado.
	originalQuery := db.query
	db.query = func(input *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
		if p, ok := input.ExpressionAttributeValues[":prefix"].(*types.AttributeValueMemberS); ok && p.Value == "EVENT#" {
			return &dynamodb.QueryOutput{}, nil
		}
		return originalQuery(input)
	}
	_, complete, err := s.receiptHistory(ctx, account)
	if err != nil || complete {
		t.Fatal("historial incompleto marcado completo", err)
	}
}
