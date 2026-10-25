package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildMetaGraphMetric(t *testing.T) {
	t.Parallel()
	started := time.Unix(1_700_000_000, 0)
	finished := started.Add(275 * time.Millisecond)

	for _, test := range []struct {
		name       string
		outcome    string
		wantFailed bool
	}{
		{name: "success", outcome: "success"},
		{name: "provider rejection", outcome: "http_4xx", wantFailed: true},
		{name: "transport failure", outcome: "transport", wantFailed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, err := buildMetaGraphMetric("dev", "send_message", test.outcome, started, finished)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err = json.Unmarshal(payload, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded["Stage"] != "dev" || decoded["Operation"] != "send_message" || decoded["Outcome"] != test.outcome || decoded["GraphApiLatency"] != float64(275) {
				t.Fatalf("unexpected metric %#v", decoded)
			}
			_, failed := decoded["GraphApiFailures"]
			if failed != test.wantFailed {
				t.Fatalf("GraphApiFailures presence = %v, want %v", failed, test.wantFailed)
			}
		})
	}
}

func TestMetaGraphStatusOutcome(t *testing.T) {
	t.Parallel()
	for status, want := range map[int]string{200: "success", 429: "http_4xx", 500: "http_5xx", 302: "http_other"} {
		if got := metaGraphStatusOutcome(status); got != want {
			t.Fatalf("metaGraphStatusOutcome(%d) = %q, want %q", status, got, want)
		}
	}
}
