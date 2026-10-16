package domain

import (
	"fmt"
	"testing"
	"time"
)

func TestBuildListSnapshotDocuments(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	items := make([]ConversationSummary, 201)
	for index := range items {
		items[index] = ConversationSummary{Phone: fmt.Sprintf("%03d", index), UnreadCount: index % 3}
	}
	documents := buildListSnapshotDocuments(ListSnapshotCreate{OwnerID: "owner", QueryHash: "query", Cutoff: 42, Items: items}, "session", 2, now)

	if documents.meta.TotalItems != 201 || documents.meta.UnreadTotal != 201 || documents.meta.ChunkCount != 3 {
		t.Fatalf("meta = %#v", documents.meta)
	}
	if documents.meta.ExpiresAt != now.Add(15*time.Minute).Unix() || !documents.meta.AbsoluteExpiresAt.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("invalid expiration: %#v", documents.meta)
	}
	wantLengths := []int{100, 100, 1}
	for index, want := range wantLengths {
		if got := len(documents.chunks[index].Items); got != want {
			t.Errorf("chunk %d length = %d, want %d", index, got, want)
		}
		if got := documents.chunks[index].SK; got != fmt.Sprintf("CHUNK#%06d", index) {
			t.Errorf("chunk %d key = %q", index, got)
		}
	}
	items[0].Phone = "changed"
	if documents.chunks[0].Items[0].Phone == "changed" {
		t.Fatal("snapshot chunk retained caller slice backing array")
	}
}

func TestRenewedSnapshotExpiryNeverExceedsAbsoluteLimit(t *testing.T) {
	absolute := time.Date(2026, 10, 14, 12, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		want int64
	}{
		{name: "regular renewal", now: absolute.Add(-20 * time.Minute), want: absolute.Add(-5 * time.Minute).Unix()},
		{name: "capped renewal", now: absolute.Add(-5 * time.Minute), want: absolute.Unix()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := renewedSnapshotExpiry(test.now, absolute); got != test.want {
				t.Fatalf("expiry = %d, want %d", got, test.want)
			}
		})
	}
}
