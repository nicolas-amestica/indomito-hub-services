package collection

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

func providerJobRecord(id, paymentID, status string) events.DynamoDBEventRecord {
	return events.DynamoDBEventRecord{EventID: id, Change: events.DynamoDBStreamRecord{NewImage: map[string]events.DynamoDBAttributeValue{
		"pk":     events.NewStringAttribute("PROVIDER_JOB#2026-10-04"),
		"sk":     events.NewStringAttribute("PENDING#" + paymentID),
		"status": events.NewStringAttribute(status),
	}}}
}

func TestProviderNotificationWorkerProcessesAndIgnoresUnrelatedRecords(t *testing.T) {
	a, db, verifier, req := webhookFixture(t)
	n := ProviderNotification{PaymentID: "abcdefghijkl", AttemptID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", ReceivedAt: a.Now()}
	if err := a.Accounts.ReceiveProviderNotification(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	worker := ProviderNotificationWorkerApp{Accounts: a.Accounts, Verifier: verifier, Zone: a.Zone, Now: a.Now}
	unrelated := providerJobRecord("ignored", "abcdefghijkl", "DONE")
	result := worker.Handle(context.Background(), events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{unrelated, providerJobRecord("job", "abcdefghijkl", "PENDING")}})
	if len(result.BatchItemFailures) != 0 {
		t.Fatalf("job failed: %+v body=%s", result, req.Body)
	}
	account, err := a.Accounts.GetAccount(context.Background(), "account")
	if err != nil || account.Installments[0].Paid != 20000 || db.commits != 5 {
		t.Fatalf("worker did not apply payment: %+v %v", account, err)
	}
}

func TestProviderNotificationWorkerReportsOnlyFailedRecord(t *testing.T) {
	a, _, verifier, _ := webhookFixture(t)
	n := ProviderNotification{PaymentID: "abcdefghijkl", AttemptID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", ReceivedAt: a.Now()}
	if err := a.Accounts.ReceiveProviderNotification(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	verifier.err = context.DeadlineExceeded
	worker := ProviderNotificationWorkerApp{Accounts: a.Accounts, Verifier: verifier, Zone: time.UTC, Now: a.Now}
	result := worker.Handle(context.Background(), events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{providerJobRecord("failed", "abcdefghijkl", "PENDING"), providerJobRecord("ignored", "bad", "PENDING")}})
	if len(result.BatchItemFailures) != 1 || result.BatchItemFailures[0].ItemIdentifier != "failed" {
		t.Fatalf("wrong retry set: %+v", result)
	}
	row, err := a.Accounts.read(context.Background(), "PROVIDER_NOTIFICATION#khipu#abcdefghijkl", "META")
	if err != nil || row.Status != "PENDING" {
		t.Fatalf("failed job was discarded: %+v %v", row, err)
	}
}
