package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestKhipuHTTPErrorsAreClassifiedWithoutRepeatingRequests(t *testing.T) {
	for _, tc := range []struct {
		status    int
		code      string
		retryable bool
	}{
		{400, "INVALID_REQUEST", false}, {401, "AUTHENTICATION_FAILED", false},
		{403, "PERMISSION_DENIED", false}, {404, "PAYMENT_NOT_FOUND", false},
		{408, "REQUEST_TIMEOUT", true}, {429, "RATE_LIMITED", true},
		{500, "SERVICE_UNAVAILABLE", true}, {502, "SERVICE_UNAVAILABLE", true},
		{503, "SERVICE_UNAVAILABLE", true}, {504, "SERVICE_UNAVAILABLE", true},
		{302, "UNEXPECTED_HTTP_STATUS", false},
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(fmt.Sprintf("%s/%d", method, tc.status), func(t *testing.T) {
				k, err := NewKhipu("private-api-key", 123)
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				k.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader(`{"private":"bank-account-payer-name"}`))}, nil
				})
				var response any
				err = k.request(context.Background(), method, "/v3/payments", nil, &response)
				var failure *KhipuError
				if !errors.As(err, &failure) || !errors.Is(err, ErrProvider) || failure.Code != tc.code || failure.Retryable != tc.retryable || calls != 1 {
					t.Fatalf("err=%v calls=%d", err, calls)
				}
				if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "bank-account") {
					t.Fatal("sensitive upstream error exposed")
				}
				if (tc.status == 429 || tc.status == 503) && !failure.RetryAt.After(time.Now().Add(59*time.Minute)) {
					t.Fatal("Retry-After discarded")
				}
			})
		}
	}
}

func TestKhipuRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw  string
		want time.Time
	}{
		{"120", now.Add(2 * time.Minute)}, {"0", now},
		{now.Add(time.Hour).Format(http.TimeFormat), now.Add(time.Hour)},
		{now.Add(-time.Hour).Format(http.TimeFormat), time.Time{}},
		{"-1", time.Time{}}, {"+60", time.Time{}}, {"1.5", time.Time{}},
		{"invalid private data", time.Time{}}, {"99999999999999999999", time.Time{}},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			if got := khipuRetryAt(tc.raw, now); !got.Equal(tc.want) {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestKhipuNetworkAndResponseFailuresAreRedacted(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		network    error
		body       string
		readError  bool
		retryable  bool
	}{
		{name: "network", code: "NETWORK_ERROR", network: errors.New("private host and credential"), retryable: true},
		{name: "timeout", code: "REQUEST_TIMEOUT", network: context.DeadlineExceeded, retryable: true},
		{name: "malformed JSON", code: "INVALID_RESPONSE", body: "private-invalid-json"},
		{name: "oversized body", code: "INVALID_RESPONSE", body: strings.Repeat("s", 65537)},
		{name: "interrupted read", code: "NETWORK_ERROR", readError: true, retryable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, _ := NewKhipu("private-key", 123)
			k.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				if tc.network != nil {
					return nil, tc.network
				}
				var reader io.Reader = strings.NewReader(tc.body)
				if tc.readError {
					reader = khipuFailingReader{}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(reader)}, nil
			})
			var result any
			err := k.request(context.Background(), http.MethodGet, "/v3/payments/abc123def456", nil, &result)
			var failure *KhipuError
			if !errors.As(err, &failure) || failure.Code != tc.code || failure.Retryable != tc.retryable || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe classification: %v", err)
			}
		})
	}
}

type khipuFailingReader struct{}

func (khipuFailingReader) Read([]byte) (int, error) { return 0, errors.New("private interrupted body") }

func TestKhipuCallerCancellationIsNotAProviderFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	k, _ := NewKhipu("key", 123)
	k.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.Canceled })
	var result any
	err := k.request(ctx, http.MethodGet, "/v3/payments/abc123def456", nil, &result)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrProvider) {
		t.Fatal("caller cancellation incorrectly reclassified")
	}
}

func TestKhipuConsultationReservesTimeForDurableRecovery(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			k, _ := NewKhipu("key", 123)
			k.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				deadline, limited := r.Context().Deadline()
				if method == http.MethodGet && (!limited || time.Until(deadline) > 10*time.Second) {
					t.Fatal("GET did not reserve recovery time")
				}
				if method == http.MethodPost && (!limited || time.Until(deadline) > 15*time.Second || time.Until(deadline) <= 10*time.Second) {
					t.Fatal("creation timeout changed")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})
			var result any
			if err := k.request(context.Background(), method, "/v3/payments", nil, &result); err != nil {
				t.Fatal(err)
			}
		})
	}
}
