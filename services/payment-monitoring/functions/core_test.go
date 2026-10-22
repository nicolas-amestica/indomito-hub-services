package functions

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func testConfig() config {
	yes := true
	return config{&yes, "alerts@example.test", "2026-09", 30, 72, 24, 20}
}

type fixture struct {
	rows                                  map[string]item
	queries                               []string
	sent                                  int
	failGet, failPut, failQuery, failSend bool
	pages                                 bool
}

func newFixture() *fixture { return &fixture{rows: map[string]item{}} }
func (f *fixture) Get(_ context.Context, pk, sk string) (item, error) {
	if f.failGet {
		return item{}, errors.New("read")
	}
	return f.rows[pk+"/"+sk], nil
}
func (f *fixture) Put(_ context.Context, row item) error {
	if f.failPut {
		return errors.New("write")
	}
	if row.PK != monitorPK {
		return errors.New("financial write")
	}
	f.rows[row.PK+"/"+row.SK] = row
	return nil
}
func (f *fixture) Query(_ context.Context, pk string, after map[string]string) (page, error) {
	if f.failQuery {
		return page{}, errors.New("query")
	}
	f.queries = append(f.queries, pk)
	if f.pages && len(after) == 0 {
		return page{Next: map[string]string{"pk": pk, "sk": "page2"}}, nil
	}
	return page{}, nil
}
func (f *fixture) Send(_ context.Context, _, _ string) error {
	if f.failSend {
		return errors.New("smtp")
	}
	f.sent++
	return nil
}

func TestConfig(t *testing.T) {
	raw, err := json.Marshal(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseConfig(string(raw), testNow); err != nil {
		t.Fatal(err)
	}
	for _, patch := range []map[string]any{{"email": "x\nbcc:x@y.cl"}, {"maxPages": 0}, {"pendingMinutes": 0}, {"startMonth": "2027-01"}, {"enabled": "true"}, {"enabled": nil}, {"maxPages": 2.5}, {"reminderHours": 169}, {"unsettledHours": 721}} {
		t.Run("invalid", func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			for k, v := range patch {
				value[k] = v
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseConfig(string(encoded), testNow); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}
func TestInspectRenditions(t *testing.T) {
	for _, tc := range []struct {
		status, since, received, want string
		fail                          bool
	}{
		{"COMPLETED", "", "", "", false}, {"OBSERVED", "", "", "OBSERVED", false}, {"REVIEW_REQUIRED", "", "", "REVIEW_REQUIRED", false},
		{"PENDING", "", "2026-10-25T10:00:00Z", "PENDING_TOO_LONG", false}, {"PENDING", "2026-10-25T12:00:00Z", "2026-10-01T00:00:00Z", "", false},
		{"PENDING", "0001-01-01T00:00:00Z", "2026-10-25T10:00:00Z", "PENDING_TOO_LONG", false}, {"PENDING", "", "bad", "", true},
	} {
		t.Run(tc.status+tc.since, func(t *testing.T) {
			f := newFixture()
			f.rows["RENDITION#r/META"] = item{Rendition: &rendition{ID: "r", Status: tc.status, PendingSince: tc.since, ReceivedAt: tc.received}}
			got, err := inspect(context.Background(), item{Rendition: &rendition{ID: "r"}}, f, testConfig(), testNow)
			if (err != nil) != tc.fail {
				t.Fatal(err)
			}
			if tc.want == "" {
				if got != nil {
					t.Fatal(got)
				}
			} else if got == nil || got.Code != tc.want {
				t.Fatal(got)
			}
		})
	}
	f := newFixture()
	if _, err := inspect(context.Background(), item{Rendition: &rendition{ID: "missing"}}, f, testConfig(), testNow); err == nil {
		t.Fatal("missing state accepted")
	}
}
func TestPayments(t *testing.T) {
	amount, partial := int64(600), int64(87)
	for _, tc := range []struct {
		name string
		s    *settlement
		want bool
		fail bool
	}{{"missing", nil, true, false}, {"complete", &settlement{&amount, &amount}, false, false}, {"partial", &settlement{&amount, &partial}, true, false}, {"malformed", &settlement{}, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.rows["TRIP#trip/SETTLEMENT#khipu:p"] = item{Settlement: tc.s}
			e := &cashEvent{Type: "PAYMENT_RECEIVED", Reference: "khipu:p", TripID: "trip", RecordedAt: "2026-10-20T12:00:00Z"}
			got, err := inspect(context.Background(), item{Event: e}, f, testConfig(), testNow)
			if (err != nil) != tc.fail || (got != nil) != tc.want {
				t.Fatalf("%v %v", got, err)
			}
			e.Reference = "bank:p"
			if got, err := inspect(context.Background(), item{Event: e}, f, testConfig(), testNow); got != nil || err != nil {
				t.Fatalf("%v %v", got, err)
			}
		})
	}
}
func TestNotifications(t *testing.T) {
	f := newFixture()
	c := testConfig()
	findings := []finding{{"OBSERVED", "r", "Rendición 42"}}
	for _, now := range []time.Time{testNow, testNow, testNow.Add(24 * time.Hour)} {
		if err := notifyFindings(context.Background(), findings, f, f, c, now); err != nil {
			t.Fatal(err)
		}
	}
	if f.sent != 2 {
		t.Fatal(f.sent)
	}
	f = newFixture()
	f.failSend = true
	if err := notifyFindings(context.Background(), findings, f, f, c, testNow); err == nil || len(f.rows) != 0 {
		t.Fatal("failed send persisted")
	}
	f = newFixture()
	f.failPut = true
	if err := notifyFindings(context.Background(), findings, f, f, c, testNow); err == nil || f.sent != 1 {
		t.Fatal("write failure hidden")
	}
}
func TestCursorCompatibility(t *testing.T) {
	f := newFixture()
	f.pages = true
	c := testConfig()
	c.MaxPages = 2
	for i := 0; i < 2; i++ {
		if err := runMonitor(context.Background(), f, f, c, testNow); err != nil {
			t.Fatal(err)
		}
	}
	if f.rows[monitorPK+"/CURSOR#HISTORY"].Kind != "CASH" {
		t.Fatal(f.rows)
	}
	c.MaxPages = 20
	if err := runMonitor(context.Background(), f, f, c, testNow); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, q := range f.queries {
		if q == "CASH#2026-09" {
			found = true
		}
	}
	if !found {
		t.Fatal("historical month skipped")
	}
	f.rows[monitorPK+"/CURSOR#CURRENT"] = item{Month: "2026-12", Kind: "CASH", After: map[string]string{"sk": "old"}}
	f.queries = nil
	if err := runMonitor(context.Background(), f, f, c, time.Date(2027, 1, 2, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if f.queries[0] != "RENDITION_MONTH#2027-01" {
		t.Fatal(f.queries)
	}
}
func TestMonitorStops(t *testing.T) {
	f := newFixture()
	f.failQuery = true
	c := testConfig()
	if err := runMonitor(context.Background(), f, f, c, testNow); err == nil || len(f.rows) != 0 {
		t.Fatal("advanced failed cursor")
	}
	*c.Enabled = false
	if err := runMonitor(context.Background(), f, f, c, testNow); err != nil {
		t.Fatal(err)
	}
	*c.Enabled = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runMonitor(ctx, f, f, c, testNow); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runMonitor(ctx, f, f, c, testNow); err != nil {
		t.Fatal(err)
	}
}
func TestDynamoWireCompatibility(t *testing.T) {
	// Documento equivalente al producido por DynamoDBDocumentClient de Node.
	raw := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: monitorPK}, "sk": &types.AttributeValueMemberS{Value: "CURSOR#HISTORY"}, "month": &types.AttributeValueMemberS{Value: "2026-09"}, "kind": &types.AttributeValueMemberS{Value: "CASH"}, "after": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "CASH#2026-09"}, "sk": &types.AttributeValueMemberS{Value: "old"}}}}
	var row item
	if err := attributevalue.UnmarshalMap(raw, &row); err != nil {
		t.Fatal(err)
	}
	if row.After["sk"] != "old" || row.Month != "2026-09" {
		t.Fatal(row)
	}
	encoded, err := attributevalue.MarshalMap(row)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := encoded["after"].(*types.AttributeValueMemberM); !ok {
		t.Fatal(encoded)
	}
	raw = map[string]types.AttributeValue{"rendition": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{"ID": &types.AttributeValueMemberS{Value: "r"}, "ProviderID": &types.AttributeValueMemberN{Value: "42"}, "Status": &types.AttributeValueMemberS{Value: "OBSERVED"}}}}
	if err := attributevalue.UnmarshalMap(raw, &row); err != nil {
		t.Fatal(err)
	}
	if row.Rendition == nil || row.Rendition.ProviderID != 42 {
		t.Fatal(row)
	}
}

func FuzzConfig(f *testing.F) {
	f.Add(`{}`)
	f.Add(`{"enabled":true}`)
	f.Fuzz(func(t *testing.T, raw string) {
		_, err := parseConfig(raw, testNow)
		if err == nil && !json.Valid([]byte(raw)) {
			t.Fatal("invalid JSON accepted")
		}
	})
}
