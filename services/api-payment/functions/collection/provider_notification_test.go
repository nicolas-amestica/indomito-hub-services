package collection

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestProviderNotificationIsDurableIdempotentAndRejectsMismatch(t *testing.T) {
	a, db, _, _ := webhookFixture(t)
	s := a.Accounts
	n := ProviderNotification{PaymentID: "abcdefghijkl", AttemptID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", ReceivedAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	if err := s.ReceiveProviderNotification(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if err := s.ReceiveProviderNotification(context.Background(), n); err != nil || db.commits != 3 {
		t.Fatalf("replay wrote again: %v commits=%d", err, db.commits)
	}
	n.AttemptID = paymentOperationID("different")
	if err := s.ReceiveProviderNotification(context.Background(), n); !errors.Is(err, ErrReplayMismatch) || db.commits != 3 {
		t.Fatalf("mismatch accepted: %v", err)
	}
}

func TestProviderNotificationRecoversLostResponsesAndConcurrentWorkers(t *testing.T) {
	a, db, v, _ := webhookFixture(t)
	s := a.Accounts
	db.lostResponse = true
	n := ProviderNotification{PaymentID: "abcdefghijkl", AttemptID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", ReceivedAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	if err := s.ReceiveProviderNotification(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ProcessProviderNotification(context.Background(), v.verifierFake, n.PaymentID, time.Now(), time.UTC)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	account, err := s.GetAccount(context.Background(), "account")
	if err != nil || account.Installments[0].Paid != 20000 || db.commits != 5 {
		t.Fatalf("cash duplicated: %+v commits=%d %v", account, db.commits, err)
	}
	row, err := s.read(context.Background(), "PROVIDER_NOTIFICATION#khipu#abcdefghijkl", "META")
	if err != nil || row.Status != "PROCESSED" {
		t.Fatalf("notification open: %+v %v", row, err)
	}
}

func TestProviderNotificationFailureKeepsJobPending(t *testing.T) {
	a, _, v, _ := webhookFixture(t)
	s := a.Accounts
	n := ProviderNotification{PaymentID: "abcdefghijkl", AttemptID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", ReceivedAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	if err := s.ReceiveProviderNotification(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	v.err = errors.New("provider unavailable")
	if _, err := s.ProcessProviderNotification(context.Background(), v, n.PaymentID, time.Now(), time.UTC); err == nil {
		t.Fatal("provider failure hidden")
	}
	row, err := s.read(context.Background(), "PROVIDER_NOTIFICATION#khipu#abcdefghijkl", "META")
	if err != nil || row.Status != "PENDING" {
		t.Fatalf("retry discarded: %+v %v", row, err)
	}
	job, err := s.read(context.Background(), "PROVIDER_JOB#2026-10-04", "PENDING#abcdefghijkl")
	if err != nil || job.Status != "PENDING" {
		t.Fatalf("pending job closed: %+v %v", job, err)
	}
	account, err := s.GetAccount(context.Background(), "account")
	if err != nil || account.Installments[0].Paid != 0 {
		t.Fatalf("failed verification applied money: %+v %v", account, err)
	}
}

func TestProviderNotificationValidatesInputBeforeWriting(t *testing.T) {
	s, db := serviceFixture(t)
	for _, n := range []ProviderNotification{{}, {PaymentID: "bad", AttemptID: paymentOperationID("a"), ReceivedAt: time.Now()}, {PaymentID: "abcdefghijkl", AttemptID: "bad", ReceivedAt: time.Now()}, {PaymentID: "abcdefghijkl", AttemptID: paymentOperationID("a")}} {
		if err := s.ReceiveProviderNotification(context.Background(), n); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("invalid input accepted: %+v %v", n, err)
		}
	}
	if db.commits != 0 {
		t.Fatal("invalid notification wrote state")
	}
}
