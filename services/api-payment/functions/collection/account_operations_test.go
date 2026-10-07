package collection

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

func operationsFixture(t *testing.T) (AccountOperationsApp, *transactionDB, events.APIGatewayV2HTTPRequest) {
	t.Helper()
	p, db, _, _ := portalFixture(t)
	req := adminRequest("POST", `{"commandId":"01ARZ3NDEKTSV4RRFFQ69G5FAW","version":1,"operation":"MANUAL_INSTALLMENT","reason":"Transferencia verificada en cartola","reference":"bank-account-001:movement-001","effectiveDate":"2026-09-30","email":"receipt@example.com"}`)
	return AccountOperationsApp{Accounts: p.Accounts, Now: func() time.Time { return time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC) }}, db, req
}

func TestAdminManualPaymentIsAtomicAndIdempotent(t *testing.T) {
	a, db, req := operationsFixture(t)
	for range 2 {
		resp, err := a.Handle(context.Background(), req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("manual: %s %v", resp.Body, err)
		}
	}
	account, err := a.Accounts.GetAccount(context.Background(), approvedID)
	if err != nil || account.Installments[0].Paid != 20000 || db.commits != 1 {
		t.Fatal("duplicated manual receipt")
	}
	id := paymentOperationID("admin:" + approvedID + ":01ARZ3NDEKTSV4RRFFQ69G5FAW")
	receipt, err := a.Accounts.read(context.Background(), "RECEIPT#"+id, "META")
	if err != nil || receipt.ReceiptEmail != "receipt@example.com" || receipt.Event.Entries[0].Account != "BANK" {
		t.Fatal("missing receipt or incorrect cash")
	}
}

func TestAdminOperationsRejectUnsafeRequestsBeforeWrites(t *testing.T) {
	for _, scenario := range []string{"anonymous", "passenger", "amount", "future", "email", "unknown-field", "stale-version", "unknown-operation", "forged-actor"} {
		t.Run(scenario, func(t *testing.T) {
			a, db, req := operationsFixture(t)
			var body map[string]any
			if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "anonymous":
				req.RequestContext.Authorizer = nil
			case "passenger":
				req.RequestContext.Authorizer.Lambda["paymentAccess"] = "passenger"
			case "amount":
				body["amount"] = 1
			case "future":
				body["effectiveDate"] = "2027-01-01"
			case "email":
				body["email"] = "invalid"
			case "unknown-field":
				body["accountId"] = "other"
			case "stale-version":
				body["version"] = 99
			case "unknown-operation":
				body["operation"] = "WITHDRAW"
			case "forged-actor":
				body["actor"] = "other"
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			req.Body = string(raw)
			response, err := a.Handle(context.Background(), req)
			if err != nil || response.StatusCode < 400 || db.commits != 0 {
				t.Fatalf("unsafe request %+v %v", response, err)
			}
		})
	}
}

func TestAdminOperationChangedReplayIsRejected(t *testing.T) {
	a, db, req := operationsFixture(t)
	if response, err := a.Handle(context.Background(), req); err != nil || response.StatusCode != 200 {
		t.Fatal("first failed")
	}
	var body AccountOperationRequest
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		t.Fatal(err)
	}
	body.Reason = "Otra razon para la misma clave"
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req.Body = string(raw)
	response, err := a.Handle(context.Background(), req)
	if err != nil || response.StatusCode != 409 || db.commits != 1 {
		t.Fatal("changed replay accepted")
	}
}

func TestAdminAllocatesReviewedFundsWithoutSecondReceipt(t *testing.T) {
	a, db, req := operationsFixture(t)
	account, err := a.Accounts.GetAccount(context.Background(), approvedID)
	if err != nil {
		t.Fatal(err)
	}
	account.UnappliedReceived = 20000
	account.ReviewAttemptID = paymentOperationID("review-attempt")
	saveLookupRow(t, db, record{PK: "ACCOUNT#" + approvedID, SK: "META", Version: account.Version, Account: &account})
	req.Body = `{"commandId":"01ARZ3NDEKTSV4RRFFQ69G5FAX","version":1,"operation":"ALLOCATE_UNAPPLIED","reason":"Fondos y cuota completa revisados","installmentIds":["0001"]}`
	response, err := a.Handle(context.Background(), req)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("allocate: %s %v", response.Body, err)
	}
	updated, _ := a.Accounts.GetAccount(context.Background(), approvedID)
	if updated.UnappliedReceived != 0 || updated.Installments[0].Paid != 20000 || updated.ReviewAttemptID != "" {
		t.Fatalf("incorrect allocation: %+v", updated)
	}
	commandID := paymentOperationID("admin:" + approvedID + ":01ARZ3NDEKTSV4RRFFQ69G5FAX")
	if _, err = a.Accounts.read(context.Background(), "RECEIPT#"+commandID, "META"); err == nil {
		t.Fatal("allocation emitted a second payment receipt")
	}
}
