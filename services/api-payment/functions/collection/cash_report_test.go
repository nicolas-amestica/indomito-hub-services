package collection

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestConsolidatedCashReadsMonthlyPartitionsWithoutInventingBalances(t *testing.T) {
	db := &transactionDB{items: map[string]map[string]types.AttributeValue{}}
	s := Service{DB: db, Table: "payments"}
	for _, event := range []domain.Event{
		{Audit: domain.Audit{CommandID: "in-a", Actor: "operator", Reason: "Ingreso verificado", RecordedAt: time.Now()}, TripID: "trip-a", EffectiveDate: "2026-09-30", Entries: []domain.Entry{{Account: "BANK", Amount: 10000}, {Account: "CUSTOMER_FUNDS", Amount: -10000}}},
		{Audit: domain.Audit{CommandID: "out-a", Actor: "operator", Reason: "Egreso verificado", RecordedAt: time.Now()}, TripID: "trip-a", EffectiveDate: "2026-10-02", Entries: []domain.Entry{{Account: "SUPPLIER_ADVANCES", Amount: 3000}, {Account: "BANK", Amount: -3000}}},
		{Audit: domain.Audit{CommandID: "in-b", Actor: "operator", Reason: "Liquidacion verificada", RecordedAt: time.Now()}, TripID: "trip-b", EffectiveDate: "2026-10-03", Entries: []domain.Entry{{Account: "BANK", Amount: 4900}, {Account: "PAYMENT_FEES", Amount: 100}, {Account: "PROVIDER_RECEIVABLE", Amount: -5000}}},
	} {
		for _, row := range cashProjectionRecords(event) {
			if row.PK[:5] == "CASH#" {
				saveLookupRow(t, db, row)
			}
		}
	}
	view, err := s.ConsolidatedCash(context.Background(), "2026-09-01", "2026-10-31")
	if err != nil || view.Inflows != 14900 || view.Outflows != 3000 || view.Net != 11900 || view.ActualFees != 100 || len(view.Trips) != 2 {
		t.Fatalf("unexpected consolidated cash: %+v %v", view, err)
	}
	if _, err = s.ConsolidatedCash(context.Background(), "2025-01-01", "2026-10-31"); err == nil {
		t.Fatal("unbounded report accepted")
	}
}

func TestTripFinancialPositionKeepsDebtAndExpectedRefundOutOfCash(t *testing.T) {
	s, _, input, _ := groupDepositFixture(t)
	supplierID := paymentOperationID("position-supplier")
	if _, _, err := s.ApplySupplierOperation(context.Background(), input.TripID, supplierID, 0, SupplierOperation{Type: "CREATE", Name: "Hotel Andes", Service: "Alojamiento", Committed: 1000}, supplierAudit("position-create")); err != nil {
		t.Fatal(err)
	}
	app := TreasuryAdminApp{Accounts: s}
	position, exposure, err := app.tripFinancialPosition(context.Background(), input.TripID)
	if err != nil || position.Receivable != 3000 || position.RefundPayable != 0 || exposure.CommittedPending != 1000 || exposure.RefundExpected != 0 {
		t.Fatalf("unexpected position: %+v %+v %v", position, exposure, err)
	}
}
