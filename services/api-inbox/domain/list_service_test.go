package domain

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSnapshotStore struct {
	created     ListSnapshotCreate
	createPage  ListSnapshotPage
	readRequest ListSnapshotPageRequest
	readPage    ListSnapshotPage
}

func (store *fakeSnapshotStore) Create(_ context.Context, input ListSnapshotCreate) (ListSnapshotPage, error) {
	store.created = input
	return store.createPage, nil
}

func (store *fakeSnapshotStore) ReadPage(_ context.Context, input ListSnapshotPageRequest) (ListSnapshotPage, error) {
	store.readRequest = input
	return store.readPage, nil
}

func TestConversationListServiceFirstPage(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	reader := newFakeListReader()
	reader.items["LIST_VERSION#00"] = []ListVersion{{Phone: "5691", ValidFromSequence: 1, Operation: "upsert", Summary: ConversationSummary{Phone: "5691", Alias: "Álvaro", UnreadCount: 3}}}
	store := &fakeSnapshotStore{createPage: ListSnapshotPage{SessionID: "session-1", Slot: 0, Page: 1, Items: []ConversationSummary{{Phone: "5691", UnreadCount: 3}}, TotalItems: 201, UnreadTotal: 9, Cutoff: 42, CreatedAt: now, AbsoluteExpiresAt: now.Add(30 * time.Minute), HasMore: true}}
	codec, _ := NewListCursorCodec([]byte("01234567890123456789012345678901"), func() time.Time { return now })
	service, err := NewConversationListService(reader, store, codec, "table", 20_000, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	response, err := service.FirstPage(context.Background(), "owner-1", " alvaro ")
	if err != nil {
		t.Fatalf("FirstPage() error = %v", err)
	}
	if store.created.Cutoff != 42 || len(store.created.Items) != 1 || store.created.QueryHash != ListQueryHash("alvaro") {
		t.Fatalf("snapshot input = %#v", store.created)
	}
	if response.PageInfo.TotalPages != 2 || response.NextCursor == nil || response.Watermark.Sequence != 42 {
		t.Fatalf("response = %#v", response)
	}
	cursor, err := codec.Decode(*response.NextCursor, "owner-1", ListQueryHash("ÁLVARO"))
	if err != nil || cursor.Page != 2 || cursor.SessionID != "session-1" {
		t.Fatalf("cursor = %#v, error = %v", cursor, err)
	}
}

func TestConversationListServiceNextPageUsesFrozenSnapshot(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(30 * time.Minute)
	reader := newFakeListReader()
	store := &fakeSnapshotStore{readPage: ListSnapshotPage{SessionID: "session-1", Slot: 2, Page: 2, TotalItems: 201, Cutoff: 40, AbsoluteExpiresAt: expiresAt}}
	codec, _ := NewListCursorCodec([]byte("01234567890123456789012345678901"), func() time.Time { return now })
	service, _ := NewConversationListService(reader, store, codec, "table", 20_000, func() time.Time { return now })
	token, _ := codec.Encode(ListCursor{Version: 1, SessionID: "session-1", Page: 2, QueryHash: ListQueryHash("q"), OwnerHash: HashCursorBinding("owner-1"), Slot: 2, IssuedAt: now.Unix(), AbsoluteExpiresAt: expiresAt.Unix()})

	response, err := service.NextPage(context.Background(), "owner-1", "q", token)
	if err != nil {
		t.Fatalf("NextPage() error = %v", err)
	}
	if store.readRequest.Page != 2 || store.readRequest.PageSize != 200 || response.NextCursor != nil {
		t.Fatalf("request = %#v, response = %#v", store.readRequest, response)
	}
	if len(reader.queries) != 0 {
		t.Fatal("continuation must not query mutable list versions")
	}
}

func TestConversationListServiceRejectsSnapshotMismatch(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(30 * time.Minute)
	store := &fakeSnapshotStore{readPage: ListSnapshotPage{SessionID: "another-session", Slot: 0, Page: 2, AbsoluteExpiresAt: expiresAt}}
	codec, _ := NewListCursorCodec([]byte("01234567890123456789012345678901"), func() time.Time { return now })
	service, _ := NewConversationListService(newFakeListReader(), store, codec, "table", 20_000, func() time.Time { return now })
	token, _ := codec.Encode(ListCursor{Version: 1, SessionID: "session-1", Page: 2, QueryHash: ListQueryHash(""), OwnerHash: HashCursorBinding("owner-1"), Slot: 0, IssuedAt: now.Unix(), AbsoluteExpiresAt: expiresAt.Unix()})

	_, err := service.NextPage(context.Background(), "owner-1", "", token)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("error = %v, want ErrInvalidCursor", err)
	}
}

func TestListQueryHashNormalizesCaseDiacriticsAndWhitespace(t *testing.T) {
	first := ListQueryHash("  ÁLVARO   Pérez ")
	second := ListQueryHash("alvaro perez")
	if first != second {
		t.Fatalf("equivalent queries produced different hashes: %q != %q", first, second)
	}
	if first == ListQueryHash("alvaro") {
		t.Fatal("different queries produced the same hash")
	}
}
