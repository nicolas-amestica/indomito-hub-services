package dteinbox

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"testing"
	"time"
)

func TestSyncRequestTracksExecutionAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store := Store{DB: &memoryDB{rows: make(map[string]map[string]types.AttributeValue)}, Table: "test"}
	job, created, err := store.BeginSyncRequest(ctx, "operator", "first", now)
	if err != nil || !created || job.Status != "PENDING" {
		t.Fatalf("begin %v %v %v", job, created, err)
	}
	other, created, err := store.BeginSyncRequest(ctx, "other", "second", now.Add(2*time.Minute))
	if err != nil || created || other.ID != "first" {
		t.Fatal("active request replaced")
	}
	calls := 0
	run := func(ctx context.Context) error {
		calls++
		current, e := store.SyncRequest(ctx, "first", now)
		if e != nil || current.Status != "RUNNING" {
			t.Fatal("worker started before RUNNING persisted")
		}
		return nil
	}
	if err := ExecuteSyncRequest(ctx, store, "first", func() time.Time { return now }, run); err != nil {
		t.Fatal(err)
	}
	if err := ExecuteSyncRequest(ctx, store, "first", func() time.Time { return now }, run); err != nil {
		t.Fatal(err)
	}
	finished, err := store.SyncRequest(ctx, "first", now)
	if err != nil || calls != 1 || finished.Status != "COMPLETED" || finished.FinishedAt == "" {
		t.Fatal("missing terminal state or duplicate worker execution")
	}
	if _, created, err := store.BeginSyncRequest(ctx, "other", "cooldown", now.Add(30*time.Second)); err != nil || created {
		t.Fatal("cooldown not enforced across operators")
	}
	if _, created, err := store.BeginSyncRequest(ctx, "other", "next", now.Add(61*time.Second)); err != nil || !created {
		t.Fatal("completed request blocked subsequent synchronization")
	}
	if _, err := store.TransitionSyncRequest(ctx, *job, "FAILED", "late response", now); !errors.Is(err, ErrSyncRequestConflict) {
		t.Fatal("stale write replaced completed result")
	}
}

func TestSyncRequestFailureBusyAndPartialAreNotSuccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runErr  error
		status  string
		wantErr bool
	}{{"failure", errors.New("Gmail unavailable"), "FAILED", true}, {"busy", ErrSyncBusy, "FAILED", false}, {"partial", ErrSyncIncomplete, "PENDING", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC()
			store := Store{DB: &memoryDB{rows: make(map[string]map[string]types.AttributeValue)}, Table: "test"}
			if _, _, err := store.BeginSyncRequest(ctx, "operator", "request", now); err != nil {
				t.Fatal(err)
			}
			err := ExecuteSyncRequest(ctx, store, "request", func() time.Time { return now }, func(context.Context) error { return tc.runErr })
			if (err != nil) != tc.wantErr {
				t.Fatalf("error %v", err)
			}
			job, err := store.SyncRequest(ctx, "request", now)
			if err != nil || job.Status != tc.status {
				t.Fatalf("job %v %v", job, err)
			}
		})
	}
}

func TestSyncRequestTimeoutAndCooldown(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store := Store{DB: &memoryDB{rows: make(map[string]map[string]types.AttributeValue)}, Table: "test"}
	job, _, err := store.BeginSyncRequest(ctx, "operator", "first", now)
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.TransitionSyncRequest(ctx, *job, "RUNNING", "", now)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := store.SyncRequest(ctx, "first", now.Add(7*time.Minute))
	if err != nil || expired.Status != "FAILED" {
		t.Fatal("timeout not persisted")
	}
	if _, err = store.TransitionSyncRequest(ctx, *job, "COMPLETED", "", now.Add(7*time.Minute)); !errors.Is(err, ErrSyncRequestConflict) {
		t.Fatal("late success overwrote timeout")
	}
	if _, created, err := store.BeginSyncRequest(ctx, "operator", "second", now.Add(7*time.Minute)); err != nil || !created {
		t.Fatal("failed job blocked next request")
	}
	pending, err := store.SyncRequest(ctx, "second", now.Add(23*time.Minute))
	if err != nil || pending.Status != "FAILED" {
		t.Fatal("queued request never expires")
	}
}
