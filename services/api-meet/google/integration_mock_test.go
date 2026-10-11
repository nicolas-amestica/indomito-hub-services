package meet

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fixedCredentials struct{}
func (fixedCredentials) GetSecure(_ context.Context, name string) (string, error) {
	if name != "/calendar" && name != "/host" {
		return "", errors.New("unknown OAuth secret")
	}
	return `{"client_id":"test-client","client_secret":"test-secret","refresh_token":"test-refresh"}`, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)
func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func mockResponse(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body: io.NopCloser(strings.NewReader(body)),
		Header: make(http.Header),
	}
}

func TestCalendarAndMeetOAuthIntegrationWithoutExternalCalls(t *testing.T) {
	calls := map[string]int{}
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "oauth2.googleapis.com":
			if req.Method != "POST" || req.URL.Path != "/token" {
				t.Errorf("unexpected OAuth request: %s %s", req.Method, req.URL)
			}
			calls["oauth"]++
			return mockResponse(200, `{"access_token":"fake-token","expires_in":3600}`), nil
		case "www.googleapis.com":
			if got := req.Header.Get("Authorization"); got != "Bearer fake-token" {
				t.Errorf("missing Google Calendar bearer token: %q", got)
			}
			if req.Method != "GET" || !strings.HasSuffix(req.URL.Path, "/events") {
				t.Errorf("unexpected Calendar request: %s %s", req.Method, req.URL)
			}
			calls["calendar"]++
			return mockResponse(200, `{"items":[{"id":"sample-event"}]}`), nil
		case "meet.googleapis.com":
			if got := req.Header.Get("Authorization"); got != "Bearer fake-token" {
				t.Errorf("missing Google Meet bearer token: %q", got)
			}
			if req.Method != "POST" || req.URL.Path != "/v2/spaces" {
				t.Errorf("unexpected Meet request: %s %s", req.Method, req.URL)
			}
			calls["meet"]++
			return mockResponse(200, `{"meetingUri":"https://meet.google.com/abc-defg-hij"}`), nil
		default:
			t.Errorf("unexpected external host %s", req.URL.Host)
			return mockResponse(500, "{}"), nil
		}
	})}

	client := Client{HTTP: httpClient, Parameters: fixedCredentials{}, OAuthParameter: "/calendar", HostOAuthParameter: "/host", CalendarID: "calendar@example.com"}
	payload, err := client.Request(context.Background(), "GET", "", nil, nil)
	if err != nil || !strings.Contains(string(payload), "sample-event") {
		t.Fatalf("Calendar mocked request: %s, %v", payload, err)
	}
	link, err := client.CreateMeetSpace(context.Background())
	if err != nil || link != "https://meet.google.com/abc-defg-hij" {
		t.Fatalf("Meet mocked request: %s, %v", link, err)
	}
	if calls["oauth"] != 2 || calls["calendar"] != 1 || calls["meet"] != 1 {
		t.Fatalf("unexpected API call counts %#v", calls)
	}
}

func TestCalendarRejectsUpstreamFailure(t *testing.T) {
	client := Client{
		CalendarID: "calendar@example.com",
		OAuthParameter: "/calendar",
		Parameters: fixedCredentials{},
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host == "oauth2.googleapis.com" {
				return mockResponse(200, `{"access_token":"fake-token"}`), nil
			}
			return mockResponse(429, `{"error":{"message":"rate limited"}}`), nil
		})},
	}
	if _, err := client.Request(context.Background(), "GET", "", nil, nil); err == nil {
		t.Fatal("Google Calendar HTTP 429 must not be reported as success")
	}
}

func TestCalendarRetriesTransientGetOnly(t *testing.T) {
	calls := 0
	client := Client{
		Parameters: fixedCredentials{},
		OAuthParameter: "/calendar",
		CalendarID: "calendar@example.com",
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host == "oauth2.googleapis.com" {
				return mockResponse(200, `{"access_token":"fake-token"}`), nil
			}
			calls++
			if calls == 1 {
				return mockResponse(503, `{"error":"unavailable"}`), nil
			}
			return mockResponse(200, `{"items":[]}`), nil
		})},
	}
	if _, err := client.Request(context.Background(), "GET", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected two GET attempts, got %d", calls)
	}
}
