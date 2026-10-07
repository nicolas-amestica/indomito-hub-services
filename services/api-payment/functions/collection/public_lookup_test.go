package collection

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/paymentaccess"
)

func lookupFixture(t *testing.T) (PublicApp, *transactionDB) {
	t.Helper()
	service, db := serviceFixture(t)
	app := PublicApp{Accounts: service, LookupSecret: strings.Repeat("test-only-", 4)}
	code, err := paymentaccess.CodeKey(app.LookupSecret, "AB23CD")
	if err != nil {
		t.Fatal(err)
	}
	rut, err := paymentaccess.RUTKey(app.LookupSecret, "12345678-5")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []record{{PK: code, SK: "META", TripID: "trip", Status: "RESERVED_APPROVED"}, {PK: "TRIP#trip", SK: rut, TripID: "trip", AccountID: "account"}} {
		saveLookupRow(t, db, row)
	}
	return app, db
}
func saveLookupRow(t *testing.T, db *transactionDB, row record) {
	t.Helper()
	raw, err := attributevalue.MarshalMap(row)
	if err != nil {
		t.Fatal(err)
	}
	db.items[itemKey(raw)] = raw
}
func lookupRequest(body string) events.APIGatewayV2HTTPRequest {
	return events.APIGatewayV2HTTPRequest{Body: body, RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "POST", SourceIP: "192.0.2.1"}}}
}

func TestPublicLookupOnlyExposesInstallments(t *testing.T) {
	app, _ := lookupFixture(t)
	response, err := app.HandleLookup(context.Background(), lookupRequest(`{"rut":"12.345.678-5","tripCode":"AB23CD"}`))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("lookup failed: %s %v", response.Body, err)
	}
	var body struct {
		Data PublicAccount `json:"data"`
	}
	if err = json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Installments) != 1 || body.Data.Installments[0].Outstanding != 20000 || body.Data.CheckoutEnabled {
		t.Fatal("unexpected public quota")
	}
	for _, sensitive := range []string{"12345678", "accountId", "tripId", "participantId", "reference", "bank", "reason", "actor", "email", "withdrawalRefund"} {
		if strings.Contains(response.Body, sensitive) {
			t.Fatalf("leaked %s", sensitive)
		}
	}
	if response.Headers["cache-control"] != "no-store" {
		t.Fatal("public data cacheable")
	}
}

func TestPublicLookupExposesConfiguredCheckoutWithoutWeakeningAccountState(t *testing.T) {
	app, db := lookupFixture(t)
	app.CheckoutEnabled = true
	response, err := app.HandleLookup(context.Background(), lookupRequest(`{"rut":"12.345.678-5","tripCode":"AB23CD"}`))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("lookup failed: %s %v", response.Body, err)
	}
	var body struct {
		Data PublicAccount `json:"data"`
	}
	if err = json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Data.CheckoutEnabled {
		t.Fatal("configured checkout remained disabled")
	}

	account, err := app.Accounts.GetAccount(context.Background(), "account")
	if err != nil {
		t.Fatal(err)
	}
	account.OpenAttemptID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	saveLookupRow(t, db, record{PK: "ACCOUNT#account", SK: "META", Version: account.Version, Account: &account})
	response, err = app.HandleLookup(context.Background(), lookupRequest(`{"rut":"12.345.678-5","tripCode":"AB23CD"}`))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("lookup with attempt failed: %s %v", response.Body, err)
	}
	if err = json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Data.CheckoutEnabled || body.Data.OpenAttemptID != account.OpenAttemptID {
		t.Fatal("feature flag hid the financial attempt lock")
	}
}

func TestPublicLookupRecoversOpenAttemptFromAccount(t *testing.T) {
	for _, active := range []bool{true, false} {
		t.Run(strconv.FormatBool(active), func(t *testing.T) {
			app, db := lookupFixture(t)
			account, err := app.Accounts.GetAccount(context.Background(), "account")
			if err != nil {
				t.Fatal(err)
			}
			account.Active = active
			if !active {
				for i := range account.Installments {
					account.Installments[i].Cancelled += account.Installments[i].Outstanding()
				}
			}
			account.OpenAttemptID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
			saveLookupRow(t, db, record{PK: "ACCOUNT#account", SK: "META", Version: account.Version, Account: &account})
			response, err := app.HandleLookup(context.Background(), lookupRequest(`{"rut":"12345678-5","tripCode":"AB23CD"}`))
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("lookup: %s %v", response.Body, err)
			}
			var body struct {
				Data PublicAccount `json:"data"`
			}
			if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
				t.Fatal(err)
			}
			if body.Data.OpenAttemptID != account.OpenAttemptID || body.Data.CheckoutEnabled {
				t.Fatal("lost open attempt or enabled checkout")
			}
		})
	}
}

func TestPublicLookupExposesReviewWithoutPrivateFinancialDetails(t *testing.T) {
	app, db := lookupFixture(t)
	a, err := app.Accounts.GetAccount(context.Background(), "account")
	if err != nil {
		t.Fatal(err)
	}
	a.UnappliedReceived = 100
	a.ReviewAttemptID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	saveLookupRow(t, db, record{PK: "ACCOUNT#account", SK: "META", Version: a.Version, Account: &a})
	response, err := app.HandleLookup(context.Background(), lookupRequest(`{"rut":"12345678-5","tripCode":"AB23CD"}`))
	if err != nil || response.StatusCode != 200 {
		t.Fatal("lookup failed")
	}
	var body struct {
		Data PublicAccount `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Data.ReviewRequired || body.Data.ReviewAttemptID != a.ReviewAttemptID || body.Data.OpenAttemptID != "" {
		t.Fatal("closed review disappeared")
	}
	if strings.Contains(response.Body, "unappliedReceived") {
		t.Fatal("internal financial detail exposed")
	}
}

func TestPublicLookupRejectsUnpublishedRevokedAndCrossTrip(t *testing.T) {
	for _, scenario := range []string{"preparing", "revoked", "wrong-trip", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			app, db := lookupFixture(t)
			code, err := paymentaccess.CodeKey(app.LookupSecret, "AB23CD")
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "preparing":
				saveLookupRow(t, db, record{PK: "TRIP#trip", SK: "META", Status: "PREPARING"})
			case "revoked":
				saveLookupRow(t, db, record{PK: code, SK: "META", TripID: "trip", Status: "REVOKED"})
			case "wrong-trip":
				saveLookupRow(t, db, record{PK: code, SK: "META", TripID: "other", Status: "RESERVED_APPROVED"})
			case "missing":
				delete(db.items, code+"/META")
			}
			response, err := app.HandleLookup(context.Background(), lookupRequest(`{"rut":"12345678-5","tripCode":"AB23CD"}`))
			if err != nil || response.StatusCode != 404 {
				t.Fatal("disclosed unavailable account")
			}
		})
	}
}

func TestPublicLookupRateLimitCannotBeResetWithFormatting(t *testing.T) {
	app, _ := lookupFixture(t)
	for i := 0; i < 6; i++ {
		body := `{"rut":"12345678-5","tripCode":"AB23CD"}`
		if i%2 == 1 {
			body = `{"rut":"12.345.678-5","tripCode":" ab23cd "}`
		}
		response, err := app.HandleLookup(context.Background(), lookupRequest(body))
		if err != nil {
			t.Fatal(err)
		}
		if i < 5 && response.StatusCode != 200 {
			t.Fatalf("early limit %d", i)
		}
		if i == 5 && response.StatusCode != 403 {
			t.Fatal("format bypassed rate limit")
		}
	}
}

func TestRateLimitIsAtomicAndTTLDoesNotResetCurrentWindow(t *testing.T) {
	app, db := lookupFixture(t)
	now := time.Date(2026, 9, 30, 12, 0, 10, 0, time.UTC)
	var successes atomic.Int32
	var group sync.WaitGroup
	for i := 0; i < 40; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if app.consumeAttempt(context.Background(), "test-limit", "network", 10, now) == nil {
				successes.Add(1)
			}
		}()
	}
	group.Wait()
	if successes.Load() > 10 || successes.Load() < 1 {
		t.Fatal("atomic limit exceeded")
	}
	for _, raw := range db.items {
		if strings.HasPrefix(raw["pk"].(*types.AttributeValueMemberS).Value, "RATE#") {
			if _, ok := raw["expiresAt"]; !ok {
				t.Fatal("temporary rate counter has no TTL")
			}
		}
	}
	if err := app.consumeAttempt(context.Background(), "test-limit", "network", 10, now.Add(time.Minute)); err != nil {
		t.Fatal("new window blocked by stale TTL record")
	}
}

func TestIPv6NetworkAndMissingSourceIP(t *testing.T) {
	a, err := networkIdentity("2001:db8:1234:abcd::1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := networkIdentity("2001:db8:1234:abcd::2")
	if err != nil || a != b {
		t.Fatal("IPv6 rotation bypass")
	}
	app, _ := lookupFixture(t)
	req := lookupRequest(`{}`)
	req.RequestContext.HTTP.SourceIP = ""
	req.Headers = map[string]string{"x-forwarded-for": "192.0.2.1"}
	response, err := app.HandleLookup(context.Background(), req)
	if err != nil || response.StatusCode != 403 {
		t.Fatal("trusted attacker-controlled forwarded IP")
	}
}
