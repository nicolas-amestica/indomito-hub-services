package renditioncapture

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

type memoryStore struct {
	hashes    map[string]string
	bodies    map[string][]byte
	fail      bool
	contended bool
}

func (s *memoryStore) Digest(_ context.Context, key string) (string, error) {
	if s.fail {
		return "", errors.New("storage unavailable")
	}
	return s.hashes[key], nil
}
func (s *memoryStore) Put(_ context.Context, key, digest string, b []byte) error {
	if s.hashes[key] != "" {
		return ErrOccupied
	}
	s.hashes[key] = digest
	s.bodies[key] = b
	if s.contended {
		return ErrOccupied
	}
	return nil
}
func fixture() (App, *memoryStore, events.APIGatewayV2HTTPRequest) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s := &memoryStore{hashes: map[string]string{}, bodies: map[string][]byte{}}
	a := App{Store: s, Until: now.Add(48 * time.Hour), Now: func() time.Time { return now }}
	r := events.APIGatewayV2HTTPRequest{Body: "opaque DRN sample", Headers: map[string]string{"Content-Type": "application/octet-stream", "Authorization": "must-not-be-stored"}}
	r.RequestContext.HTTP.Method = "POST"
	return a, s, r
}

func TestCapturePreservesBytesWithoutFinancialTrust(t *testing.T) {
	a, s, r := fixture()
	raw := []byte{0x1f, 0x8b, 0xff, 0, 1}
	r.Body = base64.StdEncoding.EncodeToString(raw)
	r.IsBase64Encoded = true
	for range 2 {
		if got := a.Handle(context.Background(), r); got.StatusCode != 200 {
			t.Fatal(got)
		}
	}
	if len(s.bodies) != 1 {
		t.Fatal("duplicate saved twice")
	}
	var e evidence
	b := s.bodies["unverified/slot-00.json"]
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	if string(e.Body) != string(raw) || e.Trust != "UNVERIFIED_EXTERNAL_INPUT" || strings.Contains(string(b), "must-not-be-stored") {
		t.Fatal("unsafe or lossy capture")
	}
}

func TestCaptureBoundsAndFailures(t *testing.T) {
	for _, scenario := range []string{"expired", "too long window", "oversized", "empty", "base64", "method", "storage", "full"} {
		t.Run(scenario, func(t *testing.T) {
			a, s, r := fixture()
			want := 503
			switch scenario {
			case "expired":
				a.Until = a.Now()
			case "too long window":
				a.Until = a.Now().Add(73 * time.Hour)
			case "oversized":
				r.Body = strings.Repeat("x", maxBody+1)
				want = 413
			case "empty":
				r.Body = ""
				want = 400
			case "base64":
				r.IsBase64Encoded = true
				r.Body = "%%%"
				want = 400
			case "method":
				r.RequestContext.HTTP.Method = "GET"
				want = 405
			case "storage":
				s.fail = true
			case "full":
				for i := range 32 {
					s.hashes[fmt.Sprintf("unverified/slot-%02d.json", i)] = "occupied"
				}
				want = 429
			}
			if got := a.Handle(context.Background(), r); got.StatusCode != want {
				t.Fatalf("got %d want %d", got.StatusCode, want)
			}
			if len(s.bodies) != 0 {
				t.Fatal("unexpected write")
			}
		})
	}
}

func TestCaptureRejectsMissingLambdaContext(t *testing.T) {
	_, _, r := fixture()
	got, err := Handler(context.Background(), r)
	if err != nil || got.StatusCode != 503 {
		t.Fatal(got, err)
	}
}

func TestCaptureConcurrentDuplicateUsesOneSlot(t *testing.T) {
	a, s, r := fixture()
	s.contended = true
	if got := a.Handle(context.Background(), r); got.StatusCode != 200 {
		t.Fatal(got)
	}
	if len(s.bodies) != 1 {
		t.Fatal("concurrent duplicate consumed multiple slots")
	}
}
