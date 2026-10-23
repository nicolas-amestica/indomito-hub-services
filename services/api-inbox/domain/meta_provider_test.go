package domain

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMetaGraphResourceURLUsesCurrentValidatedVersion(t *testing.T) {
	t.Parallel()

	got := metaGraphResourceURL("resource-id")
	want := "https://graph.facebook.com/v26.0/resource-id"
	if got != want {
		t.Fatalf("metaGraphResourceURL() = %q, want %q", got, want)
	}
}

func TestNewMetaTextProviderUsesSharedGraphVersion(t *testing.T) {
	t.Parallel()

	provider, err := NewMetaTextProvider(nil, "phone-id", "access-token")
	if err != nil {
		t.Fatalf("NewMetaTextProvider() error = %v", err)
	}
	if provider.endpoint != metaGraphResourceURL("phone-id")+"/messages" {
		t.Fatalf("unexpected message endpoint %q", provider.endpoint)
	}
	if provider.mediaEndpoint != metaGraphResourceURL("phone-id")+"/media" {
		t.Fatalf("unexpected media endpoint %q", provider.mediaEndpoint)
	}
}

func TestMetaProviderMarksInboundMessageReadWithTypingIndicator(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != metaGraphResourceURL("phone-id")+"/messages" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL)
		}
		if request.Header.Get("authorization") != "Bearer access-token" {
			t.Fatal("unexpected authorization header")
		}
		var payload struct {
			MessagingProduct string            `json:"messaging_product"`
			Status           string            `json:"status"`
			MessageID        string            `json:"message_id"`
			TypingIndicator  map[string]string `json:"typing_indicator"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.MessagingProduct != "whatsapp" || payload.Status != "read" || payload.MessageID != "wamid.1" || payload.TypingIndicator["type"] != "text" {
			t.Fatalf("unexpected payload %#v", payload)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"success":true}`)), Header: make(http.Header)}, nil
	})}
	provider, err := NewMetaTextProvider(client, "phone-id", "access-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = provider.MarkReadAndTyping(context.Background(), "wamid.1"); err != nil {
		t.Fatal(err)
	}
}

func TestMetaProviderRejectsEmptyReadReceiptMessageID(t *testing.T) {
	t.Parallel()
	provider, err := NewMetaTextProvider(nil, "phone-id", "access-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = provider.MarkReadAndTyping(context.Background(), "  "); err == nil {
		t.Fatal("expected validation error")
	}
}
