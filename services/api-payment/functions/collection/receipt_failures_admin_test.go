package collection

import (
	"context"
	"testing"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestRetryReceiptFailureOnlyRequeuesDelivery(t *testing.T) {
	s, db := serviceFixture(t)
	id := approvedID
	failed := record{PK: "RECEIPT#" + id, SK: "META", ReceiptID: id, ReceiptEmail: "payer@example.test", Status: "DELIVERY_FAILED", DeliveryAttempts: 5, LastFailureCode: "SMTP_NOT_ACCEPTED", Event: &domain.Event{AccountID: "account", Amount: 20000}}
	saveLookupRow(t, db, failed)
	audit := domain.Audit{CommandID: paymentOperationID("retry-test"), Actor: "operator", Reason: "Correo revisado por operador", RecordedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	app := AdminApp{Accounts: s}
	view, err := app.retryReceiptFailure(context.Background(), id, "", audit)
	if err != nil || view.ReceiptID != id {
		t.Fatalf("retry=%+v %v", view, err)
	}
	receipt, _ := s.read(context.Background(), "RECEIPT#"+id, "META")
	job, _ := s.read(context.Background(), "JOB#2026-10-05", "PENDING#"+id)
	if receipt.Status != "PENDING_DOCUMENT" || receipt.DeliveryAttempts != 0 || receipt.Event.Amount != 20000 || job.Status != "PENDING" {
		t.Fatal("retry altered payment or did not enqueue")
	}
	commits := db.commits
	if _, err = app.retryReceiptFailure(context.Background(), id, "", audit); err != nil || db.commits != commits {
		t.Fatal("retry replay was not idempotent")
	}
}

func TestRetryReceiptRejectsNonFailedDelivery(t *testing.T) {
	s, db := serviceFixture(t)
	id := approvedID
	saveLookupRow(t, db, record{PK: "RECEIPT#" + id, SK: "META", ReceiptID: id, Status: "SENT"})
	_, err := (AdminApp{Accounts: s}).retryReceiptFailure(context.Background(), id, "", domain.Audit{CommandID: paymentOperationID("bad-retry"), RecordedAt: time.Now()})
	if err == nil {
		t.Fatal("sent receipt was requeued")
	}
}
