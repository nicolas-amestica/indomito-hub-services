package collection

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ind-hub-api-gox-sls-pri-gh/libs/paymentaccess"
)

func sessionClaims(t *testing.T, token, secret string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("invalid JWT")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		t.Fatal("invalid signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err = json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestPassengerSessionClaims(t *testing.T) {
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	secret := strings.Repeat("s", 64)
	now := time.Now().UTC()
	a, err := issuePassengerSession(secret, id, id, "CODE#"+strings.Repeat("a", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := issuePassengerSession(secret, id, id, "CODE#"+strings.Repeat("a", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	c := sessionClaims(t, a.AccessToken, secret)
	d := sessionClaims(t, b.AccessToken, secret)
	if c["iss"] != "indomito-payments-dev" || c["aud"] != "indomito-payment-portal-dev" || c["tokenUse"] != "passenger-payment" || c["sub"] != id || c["tripId"] != id || c["jti"] == d["jti"] || c["exp"].(float64)-c["iat"].(float64) != 600 || a.ExpiresAt != now.Unix()+600 {
		t.Fatalf("invalid claims %+v", c)
	}
	for _, key := range []string{"body", "rut", "email", "tripCode", "access", "allowances"} {
		if _, ok := c[key]; ok {
			t.Fatalf("unexpected claim %s", key)
		}
	}
	for _, tc := range []struct {
		secret, id, code string
		now              time.Time
	}{
		{"short", id, "CODE#" + strings.Repeat("a", 64), now},
		{secret, "account", "CODE#" + strings.Repeat("a", 64), now},
		{secret, id, "AB23CD", now},
		{secret, id, "CODE#" + strings.Repeat("a", 64), time.Time{}},
	} {
		if _, err := issuePassengerSession(tc.secret, tc.id, id, tc.code, tc.now); err == nil {
			t.Fatal("invalid claims accepted")
		}
	}
}

func TestLookupIssuesSessionOnlyAfterSuccessfulResolution(t *testing.T) {
	a, db := lookupFixture(t)
	a.SessionSecret = strings.Repeat("s", 64)
	a.CheckoutEnabled = true
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	account, err := a.Accounts.GetAccount(context.Background(), "account")
	if err != nil {
		t.Fatal(err)
	}
	account.ID = id
	account.TripID = id
	code, err := paymentaccess.CodeKey(a.LookupSecret, "AB23CD")
	if err != nil {
		t.Fatal(err)
	}
	rut, err := paymentaccess.RUTKey(a.LookupSecret, "12345678-5")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []record{{PK: code, SK: "META", TripID: id, Status: "RESERVED_APPROVED"}, {PK: "TRIP#" + id, SK: rut, TripID: id, AccountID: id}, {PK: "TRIP#" + id, SK: "META", Status: "ACTIVE"}, {PK: "ACCOUNT#" + id, SK: "META", Version: account.Version, Account: &account}} {
		saveLookupRow(t, db, row)
	}
	response, err := a.HandleLookup(context.Background(), lookupRequest(`{"rut":"12345678-5","tripCode":"AB23CD"}`))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("lookup %v %s", err, response.Body)
	}
	var envelope struct {
		Data PublicAccount `json:"data"`
	}
	if err = json.Unmarshal([]byte(response.Body), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Session == nil || !envelope.Data.CheckoutEnabled {
		t.Fatal("session or configured checkout missing")
	}
	c := sessionClaims(t, envelope.Data.Session.AccessToken, a.SessionSecret)
	if c["sub"] != id || c["codeKey"] != code {
		t.Fatal("session scope incorrect")
	}
	for _, body := range []string{`{"rut":"12345678-5","tripCode":"ZZZZZZ"}`, `{"rut":"12345678-9","tripCode":"AB23CD"}`} {
		response, err = a.HandleLookup(context.Background(), lookupRequest(body))
		if err != nil || response.StatusCode == 200 || strings.Contains(response.Body, "accessToken") {
			t.Fatal("failed lookup issued token")
		}
	}
}
