package providers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func TestDevelopmentBankGuard(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"banks":[{"bank_id":"demo","name":"DemoBank"}]}`, true},
		{`{"banks":[]}`, false},
		{`{"banks":[{"bank_id":"live","name":"Banco real"}]}`, false},
		{`{"banks":[{"bank_id":"demo","name":"DemoBank"},{"bank_id":"live","name":"Banco real"}]}`, false},
		{`invalid`, false},
	} {
		k, err := NewKhipu("synthetic-key", 123)
		if err != nil {
			t.Fatal(err)
		}
		k.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v3/banks" || r.Method != "GET" {
				t.Fatal("incorrect request")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})
		_, err = k.DevelopmentBank(context.Background())
		if (err == nil) != tc.valid {
			t.Fatal("bank guard failed")
		}
	}
}

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestVerifyKhipuPayment(t *testing.T) {
	base := VerifiedPayment{PaymentID: "abc123def456", TransactionID: "internal-attempt", ReceiverID: 123, Amount: "20000.0000", Currency: "CLP", Status: "done", StatusDetail: "normal", ConciliationDate: time.Now().UTC()}
	for _, tc := range []struct {
		name   string
		change func(*VerifiedPayment)
		valid  bool
	}{
		{"valid", func(*VerifiedPayment) {}, true},
		{"fraction", func(p *VerifiedPayment) { p.Amount = "20000.0001" }, false},
		{"wrong amount", func(p *VerifiedPayment) { p.Amount = "10000" }, false},
		{"wrong merchant", func(p *VerifiedPayment) { p.ReceiverID = 124 }, false},
		{"wrong reference", func(p *VerifiedPayment) { p.TransactionID = "another" }, false},
		{"wrong id", func(p *VerifiedPayment) { p.PaymentID = "000000000000" }, false},
		{"wrong currency", func(p *VerifiedPayment) { p.Currency = "USD" }, false},
		{"pending", func(p *VerifiedPayment) { p.Status = "pending" }, false},
		{"refund", func(p *VerifiedPayment) { p.StatusDetail = "fully-refunded" }, false},
		{"manual", func(p *VerifiedPayment) { p.StatusDetail = "marked-paid-by-receiver" }, false},
		{"missing date", func(p *VerifiedPayment) { p.ConciliationDate = time.Time{} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payment := base
			tc.change(&payment)
			k, err := NewKhipu("synthetic-test-key", 123)
			if err != nil {
				t.Fatal(err)
			}
			k.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "payment-api.khipu.com" || r.Method != "GET" || r.Header.Get("x-api-key") != "synthetic-test-key" {
					t.Fatal("invalid request")
				}
				body, _ := json.Marshal(payment)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})
			_, err = k.Verify(context.Background(), base.PaymentID, base.TransactionID, 20000)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestCreateKhipuDoesNotRetryOrSendEmails(t *testing.T) {
	k, _ := NewKhipu("synthetic-test-key", 123)
	in := CheckoutRequest{TransactionID: "attempt", Subject: "Cuota", Amount: 20000, ReturnURL: "https://payments.example/return", CancelURL: "https://payments.example/cancel", NotifyURL: "https://api.example/webhook", ExpiresAt: time.Now().Add(time.Hour)}
	calls := 0
	k.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["send_email"] != false || body["send_reminders"] != false || body["notify_api_version"] != "3.0" || body["amount"] != float64(20000) {
			t.Fatal("unsafe payload")
		}
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("private upstream error"))}, nil
	})
	if _, err := k.Create(context.Background(), in); err != ErrProvider || calls != 1 {
		t.Fatal("ambiguous create retried")
	}
	k.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"payment_id":"abc123def456","payment_url":"https://evil.example/checkout"}`))}, nil
	})
	if _, err := k.Create(context.Background(), in); err != ErrVerification {
		t.Fatal("foreign checkout accepted")
	}
	k.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"payment_id":"abc123def456","payment_url":"https://khipu.com/payment/info/abc123def456"}`))}, nil
	})
	if _, err := k.Create(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

func TestKhipuSignatureRawBodyAndFreshness(t *testing.T) {
	now := time.Now()
	body := []byte(`{"payment_id":"abc123def456"}`)
	secret := "synthetic-webhook-secret"
	stamp := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "."))
	mac.Write(body)
	header := "t=" + stamp + ",s=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if err := VerifyKhipuSignature(body, header, secret, now); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body   []byte
		header string
		now    time.Time
	}{
		{append(append([]byte{}, body...), ' '), header, now},
		{body, header, now.Add(6 * time.Minute)},
		{body, header, now.Add(-6 * time.Minute)},
		{body, header + ",t=" + stamp, now},
		{body, "t=" + stamp + ",s=invalid", now},
	} {
		if VerifyKhipuSignature(tc.body, tc.header, secret, tc.now) == nil {
			t.Fatal("invalid signature accepted")
		}
	}
}
