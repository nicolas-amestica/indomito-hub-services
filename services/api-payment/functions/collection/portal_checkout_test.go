package collection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

func portalFixture(t *testing.T) (PortalApp, *transactionDB, *checkoutGatewayFake, events.APIGatewayV2HTTPRequest) {
	t.Helper()
	s, db := serviceFixture(t)
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	code := "CODE#" + strings.Repeat("a", 64)
	a, err := s.GetAccount(context.Background(), "account")
	if err != nil {
		t.Fatal(err)
	}
	a.ID = id
	a.TripID = id
	for _, r := range []record{{PK: "ACCOUNT#" + id, SK: "META", Version: a.Version, Account: &a}, {PK: "TRIP#" + id, SK: "META", Status: "ACTIVE"}, {PK: code, SK: "META", Status: "RESERVED_APPROVED", TripID: id}} {
		saveLookupRow(t, db, r)
	}
	req := events.APIGatewayV2HTTPRequest{Body: `{"email":"receipt@example.com"}`, Headers: map[string]string{"Idempotency-Key": "01ARZ3NDEKTSV4RRFFQ69G5FAW"}, RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "POST"}, Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]interface{}{"paymentAccess": "passenger", "paymentAccountId": id, "paymentTripId": id, "paymentSessionId": id, "paymentCodeKey": code}}}}
	gateway := &checkoutGatewayFake{}
	return PortalApp{Accounts: s, Gateway: gateway, URLs: testCheckoutURLs(), Now: time.Now}, db, gateway, req
}

func portalAttemptBody(t *testing.T, resp events.APIGatewayV2HTTPResponse) PortalAttempt {
	t.Helper()
	var body struct {
		Data PortalAttempt `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func TestPortalCheckoutAndOwnedStatus(t *testing.T) {
	a, db, gateway, req := portalFixture(t)
	ctx := context.Background()
	first := a.HandleCheckout(ctx, req)
	if first.StatusCode != 200 {
		t.Fatalf("create %s", first.Body)
	}
	view := portalAttemptBody(t, first)
	if view.Status != "PENDING_PAYMENT" || view.PaymentURL == "" || gateway.input.Amount != 20000 {
		t.Fatalf("invalid pending %+v", view)
	}
	again := a.HandleCheckout(ctx, req)
	if again.StatusCode != 200 || gateway.calls != 1 || db.commits != 3 {
		t.Fatalf("duplicate %s calls=%d commits=%d", again.Body, gateway.calls, db.commits)
	}
	req.RequestContext.HTTP.Method = "GET"
	req.PathParameters = map[string]string{"id": view.ID}
	read := a.HandleAttempt(ctx, req)
	if read.StatusCode != 200 || strings.Contains(read.Body, "receipt@example.com") {
		t.Fatalf("read %s", read.Body)
	}
	v := verifierFake{result: providers.VerifiedPayment{PaymentID: "abcdefghijkl", TransactionID: view.ID, ReceiverID: 123, Amount: "20000", Currency: "CLP", Status: "done", StatusDetail: "normal", ConciliationDate: time.Now()}}
	if _, err := a.Accounts.ConfirmProviderCheckout(ctx, v, view.ID, "abcdefghijkl", time.Now(), time.UTC); err != nil {
		t.Fatal(err)
	}
	read = a.HandleAttempt(ctx, req)
	if result := portalAttemptBody(t, read); read.StatusCode != 200 || result.Status != "CONFIRMED" || result.PaymentURL != "" {
		t.Fatalf("confirmed read %+v", result)
	}
	req.RequestContext.HTTP.Method = "POST"
	again = a.HandleCheckout(ctx, req)
	if again.StatusCode != 200 || portalAttemptBody(t, again).Status != "CONFIRMED" || gateway.calls != 1 {
		t.Fatalf("confirmed replay recreated %s", again.Body)
	}
}

func TestPortalReviewBlocksNewCheckoutAndExistingLink(t *testing.T) {
	a, db, gateway, req := portalFixture(t)
	ctx := context.Background()
	first := a.HandleCheckout(ctx, req)
	if first.StatusCode != 200 {
		t.Fatal(first.Body)
	}
	view := portalAttemptBody(t, first)
	accountID := req.RequestContext.Authorizer.Lambda["paymentAccountId"].(string)
	account, err := a.Accounts.GetAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	account.UnappliedReceived = 100
	account.ReviewAttemptID = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	saveLookupRow(t, db, record{PK: "ACCOUNT#" + account.ID, SK: "META", Version: account.Version, Account: &account})
	if response := a.HandleCheckout(ctx, req); response.StatusCode != 409 {
		t.Fatal("reused checkout while review pending")
	}
	req.Headers["Idempotency-Key"] = "01ARZ3NDEKTSV4RRFFQ69G5FAY"
	if response := a.HandleCheckout(ctx, req); response.StatusCode < 400 {
		t.Fatal("new checkout while review pending")
	}
	req.RequestContext.HTTP.Method = "GET"
	req.PathParameters = map[string]string{"id": view.ID}
	response := a.HandleAttempt(ctx, req)
	if response.StatusCode != 200 || portalAttemptBody(t, response).PaymentURL != "" || gateway.calls != 1 {
		t.Fatal("review exposed payment link or created another charge")
	}
}

func TestPortalRejectsClientAmountsAndUnauthorizedAccess(t *testing.T) {
	for _, scenario := range []string{"amount", "account", "email", "key", "unsigned", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			a, db, gateway, req := portalFixture(t)
			switch scenario {
			case "amount":
				req.Body = `{"email":"receipt@example.com","amount":1}`
			case "account":
				req.Body = `{"email":"receipt@example.com","accountId":"other"}`
			case "email":
				req.Body = `{"email":"invalid"}`
			case "key":
				req.Headers = nil
			case "unsigned":
				req.RequestContext.Authorizer = nil
			case "revoked":
				saveLookupRow(t, db, record{PK: "CODE#" + strings.Repeat("a", 64), SK: "META", Status: "REVOKED"})
			}
			resp := a.HandleCheckout(context.Background(), req)
			if resp.StatusCode < 400 || gateway.calls != 0 || db.commits != 0 {
				t.Fatalf("unsafe checkout %s", resp.Body)
			}
		})
	}
}

func TestPortalAmbiguousCreationAndChangedEmail(t *testing.T) {
	a, _, gateway, req := portalFixture(t)
	gateway.errorCreate = providers.ErrProvider
	for range 2 {
		resp := a.HandleCheckout(context.Background(), req)
		if resp.StatusCode != 202 || portalAttemptBody(t, resp).Status != "RECONCILIATION_REQUIRED" {
			t.Fatalf("ambiguous %s", resp.Body)
		}
	}
	if gateway.calls != 1 {
		t.Fatal("provider creation repeated")
	}
	req.Body = `{"email":"other@example.com"}`
	if resp := a.HandleCheckout(context.Background(), req); resp.StatusCode != 409 {
		t.Fatalf("email changed %s", resp.Body)
	}
}

func TestPortalAttemptCannotReadAnotherAccount(t *testing.T) {
	a, db, _, req := portalFixture(t)
	resp := a.HandleCheckout(context.Background(), req)
	view := portalAttemptBody(t, resp)
	row, err := a.Accounts.read(context.Background(), "ATTEMPT#"+view.ID, "META")
	if err != nil {
		t.Fatal(err)
	}
	row.Attempt.AccountID = "other"
	saveLookupRow(t, db, row)
	req.RequestContext.HTTP.Method = "GET"
	req.PathParameters = map[string]string{"id": view.ID}
	if result := a.HandleAttempt(context.Background(), req); result.StatusCode != 404 || strings.Contains(result.Body, "khipu.com") {
		t.Fatalf("foreign attempt disclosed %s", result.Body)
	}
}
