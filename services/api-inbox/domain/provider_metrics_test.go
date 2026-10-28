package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildProviderFailureMetricContainsNoCustomerData(t *testing.T) {
	t.Parallel()
	raw, err := buildProviderFailureMetric("CalendarFailures", "create_event", "prd", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["Stage"] != "prd" || payload["Operation"] != "create_event" || payload["CalendarFailures"] != float64(1) {
		t.Fatalf("unexpected metric payload %#v", payload)
	}
	for _, forbidden := range []string{"phone", "message", "email", "token"} {
		if _, exists := payload[forbidden]; exists {
			t.Fatalf("metric contains forbidden field %q", forbidden)
		}
	}
}
