package collection

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestReceiptBackfillDryRunThenDocumentOnly(t *testing.T) {
	s, db := serviceFixture(t)
	id := approvedID
	roster, _ := attributevalue.MarshalMap(record{PK: "TRIP#trip", SK: "MEMBER#account", Roster: &RosterProjection{AccountID: "account", Name: "Ana Prueba", Document: "12.345.678-5"}})
	db.items[itemKey(roster)] = roster
	event := &domain.Event{AccountID: "account", TripID: "trip", Amount: 20000, Type: "PAYMENT_RECEIVED", EffectiveDate: "2026-10-01", Audit: domain.Audit{RecordedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}}
	saveLookupRow(t, db, record{PK: "ACCOUNT#account", SK: "RECEIPT#" + id, AccountID: "account", ReceiptID: id, Event: event})
	saveLookupRow(t, db, record{PK: "RECEIPT#" + id, SK: "META", ReceiptID: id, Event: event, Status: "SENT", DocumentKey: "receipts/account/" + id + "/v1.pdf"})
	app := AdminApp{Accounts: s}
	input := ReceiptBackfillRequest{CommandID: approvedID, Reason: "Identidad verificada"}
	audit := domain.Audit{CommandID: approvedID, Actor: "operator", Reason: input.Reason, RecordedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	dry, err := app.backfillReceipts(context.Background(), "account", input, audit)
	if err != nil || dry.Eligible != 1 || dry.Enqueued != 0 {
		t.Fatalf("dry=%+v %v", dry, err)
	}
	input.Apply = true
	applied, err := app.backfillReceipts(context.Background(), "account", input, audit)
	if err != nil || applied.Enqueued != 1 {
		t.Fatalf("apply=%+v %v", applied, err)
	}
	root, _ := s.read(context.Background(), "RECEIPT#"+id, "META")
	if root.DocumentVersion != currentReceiptDocumentVersion || root.PassengerName != "Ana Prueba" || root.DeliveryMode != "DOCUMENT_ONLY" || root.Event.Amount != 20000 || root.DocumentKey != "" {
		t.Fatal("historical receipt was not safely upgraded")
	}
}
