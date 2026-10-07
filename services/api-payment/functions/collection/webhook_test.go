package collection

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

type webhookVerifier struct {
	verifierFake
	calls int
}

func (v *webhookVerifier) VerifyReceived(ctx context.Context, payment, attempt string) (providers.VerifiedPayment, error) {
	v.calls++
	return v.verifierFake.VerifyReceived(ctx, payment, attempt)
}

func signedWebhook(body, secret string, now time.Time) events.APIGatewayV2HTTPRequest {
	stamp := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "." + body))
	return events.APIGatewayV2HTTPRequest{Body: body, Headers: map[string]string{"x-khipu-signature": "t=" + stamp + ",s=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))}, RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "POST"}}}
}

func webhookFixture(t *testing.T) (WebhookApp, *transactionDB, *webhookVerifier, events.APIGatewayV2HTTPRequest) {
	t.Helper()
	s, db := serviceFixture(t)
	now := time.Now().UTC()
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	_, err := s.ReserveCheckout(context.Background(), "account", 1, domain.Audit{CommandID: "reserve", Actor: "session", Reason: "checkout", RecordedAt: now}, id, paymentOperationID("session"), "receipt@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimCheckoutDispatch(context.Background(), "account", id, "session", now); err != nil {
		t.Fatal(err)
	}
	v := &webhookVerifier{verifierFake: verifierFake{result: providers.VerifiedPayment{PaymentID: "abcdefghijkl", TransactionID: id, ReceiverID: 123, Amount: "20000", Currency: "CLP", Status: "done", StatusDetail: "normal", ConciliationDate: now}}}
	a := WebhookApp{Accounts: s, Verifier: v, Secret: "synthetic-webhook-secret", Zone: time.UTC, Now: func() time.Time { return now }}
	return a, db, v, signedWebhook(`{"payment_id":"abcdefghijkl","transaction_id":"`+id+`"}`, a.Secret, now)
}

func TestInstallmentWebhookStoresOnceForAsyncConfirmation(t *testing.T) {
	for _, encoded := range []bool{false, true} {
		t.Run(strconv.FormatBool(encoded), func(t *testing.T) {
			a, db, v, req := webhookFixture(t)
			if encoded {
				req.Body = base64.StdEncoding.EncodeToString([]byte(req.Body))
				req.IsBase64Encoded = true
			}
			for range 2 {
				resp := a.Handle(context.Background(), req)
				if resp.StatusCode != 200 || resp.Body != `{"received":true}` {
					t.Fatalf("response %+v", resp)
				}
			}
			if db.commits != 3 || v.calls != 0 {
				t.Fatalf("commits=%d verifies=%d", db.commits, v.calls)
			}
		})
	}
}

func TestInstallmentWebhookRejectsBeforeVerification(t *testing.T) {
	for _, scenario := range []string{"no signature", "tampered body", "stale", "invalid base64", "oversized", "invalid id", "get"} {
		t.Run(scenario, func(t *testing.T) {
			a, db, v, req := webhookFixture(t)
			switch scenario {
			case "no signature":
				req.Headers = nil
			case "tampered body":
				req.Body += " "
			case "stale":
				req = signedWebhook(req.Body, a.Secret, a.Now().Add(-6*time.Minute))
			case "invalid base64":
				req.IsBase64Encoded = true
				req.Body = "%%%"
			case "oversized":
				req.Body = strings.Repeat("x", 91*1024)
			case "invalid id":
				req = signedWebhook(`{"payment_id":"abcdefghijkl","transaction_id":"invalid"}`, a.Secret, a.Now())
			case "get":
				req.RequestContext.HTTP.Method = "GET"
			}
			resp := a.Handle(context.Background(), req)
			if resp.StatusCode < 400 || v.calls != 0 || db.commits != 2 {
				t.Fatalf("unsafe webhook %+v calls=%d commits=%d", resp, v.calls, db.commits)
			}
		})
	}
}

func TestInstallmentWebhookStoresButDoesNotApplyUnverifiedPayment(t *testing.T) {
	a, db, v, req := webhookFixture(t)
	v.err = providers.ErrVerification
	resp := a.Handle(context.Background(), req)
	if resp.StatusCode != 200 || db.commits != 3 || v.calls != 0 {
		t.Fatalf("authenticated notification not stored %+v", resp)
	}
	row, err := a.Accounts.read(context.Background(), "PROVIDER_NOTIFICATION#khipu#abcdefghijkl", "META")
	if err != nil || row.Status != "PENDING" {
		t.Fatalf("unverified signal lost: %+v %v", row, err)
	}
	account, err := a.Accounts.GetAccount(context.Background(), "account")
	if err != nil || account.Installments[0].Paid != 0 {
		t.Fatalf("unverified money applied: %+v %v", account, err)
	}
}

func TestInstallmentWebhookSurvivesRosterLockAndRecoversInternally(t *testing.T) {
	a, db, v, req := webhookFixture(t)
	plan := db.items["TRIP#trip/META"]
	plan["status"] = &types.AttributeValueMemberS{Value: "UPDATING_ROSTER"}
	resp := a.Handle(context.Background(), req)
	if resp.StatusCode != 200 || v.calls != 0 || db.commits != 3 {
		t.Fatalf("notification depended on provider retry: %+v calls=%d commits=%d", resp, v.calls, db.commits)
	}
	row, err := a.Accounts.read(context.Background(), "PROVIDER_NOTIFICATION#khipu#abcdefghijkl", "META")
	if err != nil || row.Status != "PENDING" {
		t.Fatalf("pending notification lost: %+v %v", row, err)
	}
	plan["status"] = &types.AttributeValueMemberS{Value: "ACTIVE"}
	change, err := a.Accounts.ProcessProviderNotification(context.Background(), v, "abcdefghijkl", a.Now(), a.Zone)
	if err != nil || change.Event.Type != "PAYMENT_RECEIVED" || change.Account.Installments[0].Paid != 20000 || db.commits != 5 {
		t.Fatalf("recovery failed: %+v commits=%d %v", change, db.commits, err)
	}
	row, err = a.Accounts.read(context.Background(), "PROVIDER_NOTIFICATION#khipu#abcdefghijkl", "META")
	if err != nil || row.Status != "PROCESSED" {
		t.Fatalf("inbox not closed: %+v %v", row, err)
	}
}
