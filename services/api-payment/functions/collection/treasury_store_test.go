package collection

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func treasuryAudit(id string) domain.Audit {
	return domain.Audit{CommandID: paymentOperationID(id), Actor: "treasury-operator", Reason: "Cartola bancaria verificada", RecordedAt: time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)}
}

func TestReconcileSettlementPersistsExactCashAndIsIdempotent(t *testing.T) {
	s, db := serviceFixture(t)
	paymentReference := "khipu:abcdefghijkl"
	saveLookupRow(t, db, record{PK: "TRIP#trip", SK: "SETTLEMENT#" + paymentReference, TripID: "trip", Version: 1, Settlement: &domain.Settlement{PaymentReference: paymentReference, TripID: "trip", Amount: 20000, Version: 1}})
	audit := treasuryAudit("settlement-one")
	settlement, event, err := s.ReconcileSettlement(context.Background(), "trip", paymentReference, 1, 20000, 500, "bank:settlement-001", "2026-10-05", audit)
	if err != nil || settlement.SettledGross != 20000 || settlement.ActualFees != 500 || event.Type != "PAYMENT_SETTLED" {
		t.Fatalf("settlement failed: %+v %+v %v", settlement, event, err)
	}
	if db.items["TRIP#trip/CASH#2026-10-05#"+audit.CommandID] == nil || db.items["CASH#2026-10/TRIP#trip#2026-10-05#"+audit.CommandID] == nil {
		t.Fatal("cash projections were not persisted atomically")
	}
	commits := db.commits
	again, _, err := s.ReconcileSettlement(context.Background(), "trip", paymentReference, 1, 20000, 500, "bank:settlement-001", "2026-10-05", audit)
	if err != nil || again.Version != settlement.Version || db.commits != commits {
		t.Fatalf("idempotent retry changed settlement: %+v %v commits=%d", again, err, db.commits)
	}
}

func TestSettlementBankReferenceCannotBeReused(t *testing.T) {
	s, db := serviceFixture(t)
	for index := 1; index <= 2; index++ {
		paymentReference := fmt.Sprintf("khipu:payment%04d", index)
		saveLookupRow(t, db, record{PK: "TRIP#trip", SK: "SETTLEMENT#" + paymentReference, TripID: "trip", Version: 1, Settlement: &domain.Settlement{PaymentReference: paymentReference, TripID: "trip", Amount: 1000, Version: 1}})
	}
	if _, _, err := s.ReconcileSettlement(context.Background(), "trip", "khipu:payment0001", 1, 1000, 0, "bank:same-reference", "2026-10-05", treasuryAudit("first-settlement")); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.ReconcileSettlement(context.Background(), "trip", "khipu:payment0002", 1, 1000, 0, "bank:same-reference", "2026-10-05", treasuryAudit("second-settlement"))
	if !errors.Is(err, ErrReferenceUsed) {
		t.Fatalf("duplicate bank reference accepted: %v", err)
	}
}

func TestListSettlementsUsesBoundedCursorPagination(t *testing.T) {
	s, db := serviceFixture(t)
	tripID := approvedID
	for index := 0; index < 21; index++ {
		paymentReference := fmt.Sprintf("khipu:payment%04d", index)
		saveLookupRow(t, db, record{PK: "TRIP#" + tripID, SK: "SETTLEMENT#" + paymentReference, TripID: tripID, Version: 1, Settlement: &domain.Settlement{PaymentReference: paymentReference, TripID: tripID, Amount: 1000, Version: 1}})
	}
	first, err := s.ListSettlements(context.Background(), tripID, "")
	if err != nil || len(first.Items) != 20 || first.NextCursor == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	second, err := s.ListSettlements(context.Background(), tripID, first.NextCursor)
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("second page: %+v %v", second, err)
	}
}
