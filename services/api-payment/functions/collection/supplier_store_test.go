package collection

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func supplierFixture(t *testing.T) (Service, *transactionDB, string, string) {
	t.Helper()
	db := &transactionDB{items: map[string]map[string]types.AttributeValue{}}
	s := Service{DB: db, Table: "payments"}
	tripID, supplierID := approvedID, paymentOperationID("supplier-one")
	saveLookupRow(t, db, record{PK: "TRIP#" + tripID, SK: "META", Status: "ACTIVE"})
	return s, db, tripID, supplierID
}

func supplierAudit(id string) domain.Audit {
	return domain.Audit{CommandID: paymentOperationID(id), Actor: "operator", Reason: "Respaldo financiero revisado", RecordedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
}

func TestSupplierLifecycleSeparatesAgreementFromBankCash(t *testing.T) {
	s, db, tripID, supplierID := supplierFixture(t)
	created, event, err := s.ApplySupplierOperation(context.Background(), tripID, supplierID, 0, SupplierOperation{Type: "CREATE", Name: "Hotel Andes", Service: "Alojamiento", Committed: 100000}, supplierAudit("create-supplier"))
	if err != nil || created.Version != 1 || len(event.Entries) != 0 {
		t.Fatalf("create: %+v %+v %v", created, event, err)
	}
	if db.items["TRIP#"+tripID+"/CASH#"] != nil {
		t.Fatal("supplier agreement invented cash")
	}
	paid, payEvent, err := s.ApplySupplierOperation(context.Background(), tripID, supplierID, created.Version, SupplierOperation{Type: "PAY", Amount: 60000, Reference: "bank:hotel-payment", EffectiveDate: "2026-10-05"}, supplierAudit("pay-supplier"))
	if err != nil || paid.Paid != 60000 || payEvent.Entries[1].Account != "BANK" || payEvent.Entries[1].Amount != -60000 {
		t.Fatalf("pay: %+v %+v %v", paid, payEvent, err)
	}
	revised, reviseEvent, err := s.ApplySupplierOperation(context.Background(), tripID, supplierID, paid.Version, SupplierOperation{Type: "REVISE", Committed: 50000, RefundAgreed: 10000, AnnexID: "annex-services-001"}, supplierAudit("revise-supplier"))
	if err != nil || revised.RefundAgreed != 10000 || len(reviseEvent.Entries) != 0 {
		t.Fatalf("revise: %+v %+v %v", revised, reviseEvent, err)
	}
	refunded, refundEvent, err := s.ApplySupplierOperation(context.Background(), tripID, supplierID, revised.Version, SupplierOperation{Type: "RECEIVE_REFUND", Amount: 8000, Reference: "bank:hotel-refund", EffectiveDate: "2026-10-05"}, supplierAudit("refund-supplier"))
	if err != nil || refunded.RefundReceived != 8000 || refundEvent.Entries[0].Account != "BANK" {
		t.Fatalf("refund: %+v %+v %v", refunded, refundEvent, err)
	}
	if db.items["TRIP#"+tripID+"/CASH#2026-10-05#"+payEvent.CommandID] == nil || db.items["TRIP#"+tripID+"/CASH#2026-10-05#"+refundEvent.CommandID] == nil {
		t.Fatal("supplier bank movements missing from cash projection")
	}
}

func TestSupplierCommandsAreIdempotentAndBankReferencesUnique(t *testing.T) {
	s, db, tripID, supplierID := supplierFixture(t)
	audit := supplierAudit("create-supplier")
	operation := SupplierOperation{Type: "CREATE", Name: "Transportes Sur", Service: "Bus", Committed: 50000}
	first, _, err := s.ApplySupplierOperation(context.Background(), tripID, supplierID, 0, operation, audit)
	if err != nil {
		t.Fatal(err)
	}
	commits := db.commits
	again, _, err := s.ApplySupplierOperation(context.Background(), tripID, supplierID, 0, operation, audit)
	if err != nil || again.Version != first.Version || db.commits != commits {
		t.Fatal("supplier replay changed state")
	}
	paid, _, err := s.ApplySupplierOperation(context.Background(), tripID, supplierID, first.Version, SupplierOperation{Type: "PAY", Amount: 1000, Reference: "bank:unique", EffectiveDate: "2026-10-05"}, supplierAudit("first-payment"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.ApplySupplierOperation(context.Background(), tripID, supplierID, paid.Version, SupplierOperation{Type: "PAY", Amount: 1000, Reference: "bank:unique", EffectiveDate: "2026-10-05"}, supplierAudit("second-payment"))
	if !errors.Is(err, ErrReferenceUsed) {
		t.Fatalf("duplicate supplier reference accepted: %v", err)
	}
}
