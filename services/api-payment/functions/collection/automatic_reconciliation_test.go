package collection

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestAutomaticReconciliationBackoffIsBounded(t *testing.T) {
	want := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for index, expected := range want {
		if got := automaticReconciliationDelay(int64(index)); got != expected {
			t.Fatalf("attempt %d: got %s want %s", index, got, expected)
		}
	}
}

func TestAutomaticReconciliationKeepsPendingWorkAndFinishesConfirmed(t *testing.T) {
	for _, age := range []time.Duration{time.Minute, 26 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			ctx := context.Background()
			s, db := dispatchFixture(t)
			created := time.Now().UTC().Add(-age)
			if _, err := s.CreateCheckout(ctx, &checkoutGatewayFake{}, "dev", "account", "attempt", "session", testCheckoutURLs(), created); err != nil {
				t.Fatal(err)
			}
			now := created.Add(age)
			jobSK := reconciliationJobSK(created.Add(time.Minute), "attempt")
			message := reconciliationMessage{AttemptID: "attempt", AccountID: "account", JobSK: jobSK}
			v := verifierFake{}
			v.result.Amount, v.result.Status, v.result.StatusDetail = "20000", "pending", "pending"
			worker := AutomaticReconciliationApp{Accounts: s, Inspector: v, Verifier: v, Zone: time.UTC, Now: func() time.Time { return now }}
			if err := worker.process(ctx, message); err != nil {
				t.Fatal(err)
			}
			observation, err := s.read(ctx, "ATTEMPT#attempt", "PROVIDER_STATUS")
			want := "PENDING_PAYMENT"
			if age > 24*time.Hour {
				want = "PROVIDER_REVIEW_REQUIRED"
			}
			if err != nil || observation.Status != want {
				t.Fatalf("observation=%+v err=%v", observation, err)
			}
			if db.items[reconciliationPendingPK+"/"+jobSK] != nil {
				t.Fatal("old job retained")
			}
			message.JobSK, message.Count = reconciliationJobSK(now.Add(2*time.Minute), "attempt"), 1
			if db.items[reconciliationPendingPK+"/"+message.JobSK] == nil {
				t.Fatal("pending work abandoned")
			}
			account, err := s.GetAccount(ctx, "account")
			if err != nil || account.OpenAttemptID != "attempt" || account.Installments[0].Paid != 0 {
				t.Fatal("ambiguous payment released")
			}
			v.result.Status, v.result.StatusDetail = "done", "normal"
			v.result.PaymentID, v.result.TransactionID = "abcdefghijkl", "attempt"
			v.result.ReceiverID, v.result.Currency = 123, "CLP"
			v.result.ConciliationDate = now
			worker.Inspector, worker.Verifier = v, v
			now = now.Add(2 * time.Minute)
			for range 2 {
				if err := worker.process(ctx, message); err != nil {
					t.Fatal(err)
				}
			}
			account, err = s.GetAccount(ctx, "account")
			if err != nil || account.Installments[0].Paid != 20000 || account.OpenAttemptID != "" {
				t.Fatal("late payment not reconciled exactly once")
			}
		})
	}
}

func TestAutomaticReconciliationReportsAndLogsMalformedMessages(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	result := (AutomaticReconciliationApp{}).handleBatch(context.Background(), events.SQSEvent{Records: []events.SQSMessage{{MessageId: "bad", Body: `{"secret":"must-not-be-logged"}`}}}, zap.New(core))
	if len(result.BatchItemFailures) != 1 || result.BatchItemFailures[0].ItemIdentifier != "bad" || logs.Len() != 1 {
		t.Fatalf("failure not observable: %+v", result)
	}
	fields := logs.All()[0].ContextMap()
	if fields["error"] == nil || fields["messageId"] != "bad" || fields["body"] != nil {
		t.Fatalf("unexpected log fields: %+v", fields)
	}
}

func TestAutomaticReconciliationRejectsMissingCheckoutPayload(t *testing.T) {
	s, db, v := confirmationFixture(t)
	job := record{PK: reconciliationPendingPK, SK: "job", AttemptID: "attempt", AccountID: "account", Status: "PENDING"}
	for _, row := range []record{job, {PK: "ATTEMPT#attempt", SK: "CHECKOUT", AccountID: "account"}} {
		item, err := attributevalue.MarshalMap(row)
		if err != nil {
			t.Fatal(err)
		}
		db.items[row.PK+"/"+row.SK] = item
	}
	worker := AutomaticReconciliationApp{Accounts: s, Inspector: v, Verifier: v, Zone: time.UTC, Now: time.Now}
	if err := worker.process(context.Background(), reconciliationMessage{AttemptID: "attempt", AccountID: "account", JobSK: "job"}); err == nil {
		t.Fatal("invalid checkout acknowledged")
	}
}

func TestDecodeReconciliationMessageRejectsIncompleteWork(t *testing.T) {
	if _, err := decodeReconciliationMessage(`{"attemptId":"attempt"}`); err == nil {
		t.Fatal("incomplete job accepted")
	}
	message, err := decodeReconciliationMessage(`{"attemptId":"01M4C8EFRFHRK7DJW09DT6J6CX","accountId":"account","jobSk":"job","count":0}`)
	if err != nil || message.AccountID != "account" {
		t.Fatalf("valid job rejected: %+v %v", message, err)
	}
}

type reconciliationQueueFake struct{ input *sqs.SendMessageInput }

func (f *reconciliationQueueFake) SendMessage(_ context.Context, input *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.input = input
	return &sqs.SendMessageOutput{}, nil
}

func TestReconciliationDispatcherUsesDelayedQueueWithoutPolling(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	queue := &reconciliationQueueFake{}
	event := events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{{EventID: "event", Change: events.DynamoDBStreamRecord{NewImage: map[string]events.DynamoDBAttributeValue{
		"pk":            events.NewStringAttribute(reconciliationPendingPK),
		"sk":            events.NewStringAttribute("job"),
		"attemptId":     events.NewStringAttribute("01M4C8EFRFHRK7DJW09DT6J6CX"),
		"accountId":     events.NewStringAttribute("account"),
		"status":        events.NewStringAttribute("PENDING"),
		"nextAttemptAt": events.NewNumberAttribute("1791460860"),
	}}}}}
	result := (ReconciliationDispatcherApp{Queue: queue, QueueURL: "queue", Now: func() time.Time { return now }}).Handle(context.Background(), event)
	if len(result.BatchItemFailures) != 0 || queue.input == nil || queue.input.DelaySeconds != 60 {
		t.Fatalf("unexpected dispatch: %+v %+v", result, queue.input)
	}
}
