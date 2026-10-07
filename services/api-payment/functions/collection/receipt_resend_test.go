package collection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func portalResendFixture(t *testing.T) (ReceiptsApp, *transactionDB, events.APIGatewayV2HTTPRequest, string) {
	t.Helper()
	p, db, _, req := portalFixture(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	a := ReceiptsApp{Accounts: p.Accounts, Now: func() time.Time { return now }}
	reference := "khipu:abcdefghijkl"
	receiptID := paymentOperationID(reference)
	event := &domain.Event{Audit: domain.Audit{RecordedAt: now}, AccountID: approvedID, TripID: approvedID, AttemptID: approvedID, Reference: reference, Amount: 20000, Type: "PAYMENT_RECEIVED", EffectiveDate: "2026-10-01"}
	attempt := &domain.Attempt{ID: approvedID, AccountID: approvedID, Email: "original@example.test"}
	for _, row := range []record{
		{PK: "ATTEMPT#" + approvedID, SK: "META", Attempt: attempt, PaymentSessionID: approvedID},
		{PK: "ATTEMPT#" + approvedID, SK: "OUTCOME", AccountID: approvedID, Event: event},
		{PK: "RECEIPT#" + receiptID, SK: "META", ReceiptID: receiptID, ReceiptEmail: "original@example.test", Status: "SENT", Event: event, DocumentKey: "receipts/" + approvedID + "/" + receiptID + "/v1.pdf", DocumentSHA256: strings.Repeat("a", 64)},
	} {
		saveLookupRow(t, db, row)
	}
	req.RequestContext.HTTP.Method = "POST"
	req.PathParameters = map[string]string{"id": approvedID}
	req.Body = `{"commandId":"` + paymentOperationID("first-resend") + `","email":"other@example.test"}`
	return a, db, req, receiptID
}

func TestPortalReceiptResendIsSessionBoundAndOneTime(t *testing.T) {
	a, db, req, receiptID := portalResendFixture(t)
	before, err := a.Accounts.GetAccount(context.Background(), approvedID)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if response := a.HandlePortalResend(context.Background(), req); response.StatusCode != 202 {
			t.Fatalf("resend %d %s", response.StatusCode, response.Body)
		}
	}
	if db.commits != 1 {
		t.Fatalf("idempotent replay created %d transactions", db.commits)
	}
	root, _ := a.Accounts.read(context.Background(), "RECEIPT#"+receiptID, "META")
	if root.ReceiptEmail != "original@example.test" || root.Status != "SENT" {
		t.Fatal("original receipt changed")
	}
	after, _ := a.Accounts.GetAccount(context.Background(), approvedID)
	left, _ := json.Marshal(before)
	right, _ := json.Marshal(after)
	if string(left) != string(right) {
		t.Fatal("financial account changed")
	}
	req.Body = `{"commandId":"` + paymentOperationID("second-resend") + `","email":"another@example.test"}`
	if response := a.HandlePortalResend(context.Background(), req); response.StatusCode != 409 {
		t.Fatalf("second portal resend accepted: %d", response.StatusCode)
	}
	req.RequestContext.Authorizer.Lambda["paymentSessionId"] = paymentOperationID("new-session")
	if response := a.HandlePortalResend(context.Background(), req); response.StatusCode != 404 {
		t.Fatalf("new session recovered old receipt: %d", response.StatusCode)
	}
}

func TestPortalReceiptResendRejectsUnsafeCases(t *testing.T) {
	for _, scenario := range []string{"review", "document-pending", "wrong-attempt", "bad-email"} {
		t.Run(scenario, func(t *testing.T) {
			a, db, req, receiptID := portalResendFixture(t)
			switch scenario {
			case "review":
				row, _ := a.Accounts.read(context.Background(), "ATTEMPT#"+approvedID, "OUTCOME")
				row.Event.Type = "PAYMENT_REQUIRES_REVIEW"
				saveLookupRow(t, db, row)
			case "document-pending":
				row, _ := a.Accounts.read(context.Background(), "RECEIPT#"+receiptID, "META")
				row.DocumentKey = ""
				saveLookupRow(t, db, row)
			case "wrong-attempt":
				row, _ := a.Accounts.read(context.Background(), "RECEIPT#"+receiptID, "META")
				row.Event.AttemptID = paymentOperationID("other-attempt")
				saveLookupRow(t, db, row)
			case "bad-email":
				req.Body = strings.ReplaceAll(req.Body, "other@example.test", "Display <other@example.test>")
			}
			if response := a.HandlePortalResend(context.Background(), req); response.StatusCode < 400 || db.commits != 0 {
				t.Fatalf("unsafe resend queued: %d", response.StatusCode)
			}
		})
	}
}

func TestAdminReceiptResendHasNoPassengerLimit(t *testing.T) {
	a, db, _, receiptID := portalResendFixture(t)
	for i := range 5 {
		req := events.APIGatewayV2HTTPRequest{
			Body:           `{"commandId":"` + paymentOperationID(string(rune('a'+i))) + `","email":"admin-target@example.test"}`,
			PathParameters: map[string]string{"accountId": approvedID, "id": receiptID},
			RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "POST"}, Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]any{"userId": "operator", "paymentAccess": "admin"}}},
		}
		if response := a.HandleAdminResend(context.Background(), req); response.StatusCode != 202 {
			t.Fatalf("admin resend %d: %d %s", i, response.StatusCode, response.Body)
		}
	}
	if db.commits != 5 {
		t.Fatalf("admin resends committed %d", db.commits)
	}
}
