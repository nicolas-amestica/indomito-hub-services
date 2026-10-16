package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestListCursorCodecRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
	codec, err := NewListCursorCodec([]byte(strings.Repeat("s", 32)), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewListCursorCodec devolvio error: %v", err)
	}
	want := ListCursor{
		Version: 1, SessionID: "01KSESSION", Page: 2, QueryHash: "query-hash",
		OwnerHash: HashCursorBinding("user-1"), Slot: 1,
		IssuedAt: now.Unix(), AbsoluteExpiresAt: now.Add(30 * time.Minute).Unix(),
	}
	token, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("Encode devolvio error: %v", err)
	}
	got, err := codec.Decode(token, "user-1", "query-hash")
	if err != nil {
		t.Fatalf("Decode devolvio error: %v", err)
	}
	if got != want {
		t.Fatalf("cursor = %#v, want %#v", got, want)
	}
}

func TestListCursorCodecRejectsTamperingAndBindingMismatch(t *testing.T) {
	now := time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
	codec, _ := NewListCursorCodec([]byte(strings.Repeat("s", 32)), func() time.Time { return now })
	token, _ := codec.Encode(ListCursor{
		Version: 1, SessionID: "01KSESSION", Page: 2, QueryHash: "query-hash",
		OwnerHash: HashCursorBinding("user-1"), Slot: 0,
		IssuedAt: now.Unix(), AbsoluteExpiresAt: now.Add(time.Minute).Unix(),
	})
	tests := []struct {
		name  string
		token string
		owner string
		query string
	}{
		{name: "firma alterada", token: token[:len(token)-1] + "A", owner: "user-1", query: "query-hash"},
		{name: "otro owner", token: token, owner: "user-2", query: "query-hash"},
		{name: "otra consulta", token: token, owner: "user-1", query: "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := codec.Decode(tt.token, tt.owner, tt.query); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("error = %v, want ErrInvalidCursor", err)
			}
		})
	}
}

func TestListCursorCodecReportsAuthenticExpiration(t *testing.T) {
	now := time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
	codec, _ := NewListCursorCodec([]byte(strings.Repeat("s", 32)), func() time.Time { return now })
	token, _ := codec.Encode(ListCursor{
		Version: 1, SessionID: "01KSESSION", Page: 2, QueryHash: "query-hash",
		OwnerHash: HashCursorBinding("user-1"), Slot: 0,
		IssuedAt: now.Add(-2 * time.Minute).Unix(), AbsoluteExpiresAt: now.Add(-time.Minute).Unix(),
	})
	if _, err := codec.Decode(token, "user-1", "query-hash"); !errors.Is(err, ErrExpiredCursor) {
		t.Fatalf("error = %v, want ErrExpiredCursor", err)
	}
}

func TestNewListCursorCodecRejectsShortSecret(t *testing.T) {
	if _, err := NewListCursorCodec([]byte("short"), nil); err == nil {
		t.Fatal("NewListCursorCodec acepto un secreto corto")
	}
}

func TestListCursorCodecAcceptsPreviousSecretAfterRotation(t *testing.T) {
	now := time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
	previous := []byte(strings.Repeat("p", 32))
	oldCodec, _ := NewListCursorCodec(previous, func() time.Time { return now })
	token, _ := oldCodec.Encode(ListCursor{Version: 1, SessionID: "session", Page: 2, QueryHash: "query", OwnerHash: HashCursorBinding("owner"), Slot: 0, IssuedAt: now.Unix(), AbsoluteExpiresAt: now.Add(time.Minute).Unix()})
	rotatedCodec, _ := NewListCursorCodecWithPrevious([]byte(strings.Repeat("n", 32)), previous, func() time.Time { return now })
	if _, err := rotatedCodec.Decode(token, "owner", "query"); err != nil {
		t.Fatalf("previous secret cursor rejected: %v", err)
	}
}
