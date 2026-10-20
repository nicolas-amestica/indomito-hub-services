package collection

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRenditionWorkerStopsAfterTwelveFailures(t *testing.T) {
	s, db, objects, report, now := renditionFixture(t, 1)
	policy := testRenditionApplyPolicy()
	id, err := s.ReceiveRendition(context.Background(), objects, "dev", report, []byte("signed"), policy, now)
	if err != nil {
		t.Fatal(err)
	}
	// Una evidencia temporalmente inaccesible nunca debe producir caja.
	objects.values = map[string][]byte{}
	clock := now.Add(time.Second)
	app := RenditionApp{Accounts: s, Objects: objects, Policy: policy, Now: func() time.Time { return clock }}
	for attempt := 1; attempt <= 12; attempt++ {
		if app.Run(context.Background()) == nil {
			t.Fatal("failure hidden from monitoring")
		}
		row, readErr := s.read(context.Background(), renditionPK(id), "META")
		if readErr != nil {
			t.Fatal(readErr)
		}
		if row.Rendition.Retries != attempt || row.Rendition.Applied != 0 {
			t.Fatal("retry count or financial state incorrect")
		}
		if attempt == 12 && row.Rendition.Status != "REVIEW_REQUIRED" {
			t.Fatal("retry limit did not require review")
		}
		clock = clock.Add(6 * time.Minute)
	}
	for key := range db.items {
		if strings.HasPrefix(key, "RENDITION_JOBS/") {
			t.Fatal("exhausted job still scheduled")
		}
	}
	if err = app.Run(context.Background()); err != nil {
		t.Fatal("idle worker failed", err)
	}
}
