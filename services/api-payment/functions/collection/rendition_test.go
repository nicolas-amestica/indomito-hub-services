package collection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-lambda-go/events"
	"strings"
	"sync"
	"testing"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

type renditionMemoryObjects struct {
	mu     sync.Mutex
	values map[string][]byte
	fail   bool
}

func (m *renditionMemoryObjects) Put(_ context.Context, k string, b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("storage unavailable")
	}
	m.values[k] = append([]byte(nil), b...)
	return nil
}
func (m *renditionMemoryObjects) Get(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.values[k]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), b...), nil
}

func renditionFixture(t *testing.T, n int) (Service, *transactionDB, *renditionMemoryObjects, providers.Rendition, time.Time) {
	t.Helper()
	s, db := serviceFixture(t)
	objects := &renditionMemoryObjects{values: map[string][]byte{}}
	now := time.Date(2026, 10, 17, 15, 0, 0, 0, time.UTC)
	r := providers.Rendition{ID: 1, RecipientUID: "merchant", Bank: "test-bank", Account: "test-account", StartDate: "2026-10-17T00:00:00Z", EndDate: "2026-10-17T12:00:00Z"}
	for i := 0; i < n; i++ {
		payment := fmt.Sprintf("pay%09d", i)
		attemptID := paymentOperationID(fmt.Sprintf("rendition-fixture-attempt-%d", i))
		accountID := paymentOperationID(fmt.Sprintf("rendition-fixture-account-%d", i))
		tripID := paymentOperationID(fmt.Sprintf("rendition-fixture-trip-%d", i/44))
		r.Items = append(r.Items, providers.RenditionItem{PaymentID: payment, AttemptID: attemptID, PaymentDate: "2026-10-17T10:00:00Z", Amount: "10000.0000", Fee: "100", FeeScheme: "test-deducted"})
		saveLookupRow(t, db, record{PK: "ATTEMPT#" + attemptID, SK: "META", Attempt: &domain.Attempt{ID: attemptID, AccountID: accountID, Amount: 10000}})
		saveLookupRow(t, db, record{PK: "ACCOUNT#" + accountID, SK: "META", Account: &domain.Account{ID: accountID, TripID: tripID}})
		saveLookupRow(t, db, record{PK: "ATTEMPT#" + attemptID, SK: "OUTCOME", Event: &domain.Event{Type: "PAYMENT_RECEIVED", AccountID: accountID, TripID: tripID, Amount: 10000, Reference: "khipu:" + payment}})
		saveLookupRow(t, db, record{PK: "TRIP#" + tripID, SK: "SETTLEMENT#khipu:" + payment, Version: 1, Settlement: &domain.Settlement{PaymentReference: "khipu:" + payment, TripID: tripID, Amount: 10000, Version: 1}})
	}
	return s, db, objects, r, now
}

func testRenditionApplyPolicy() RenditionPolicy {
	return RenditionPolicy{Mode: "APPLY", Revision: "test-v1", ValidatedBy: "test-operator", Evidence: "test-comparison", EffectiveDateSource: "REPORT_END", DeductedFeeSchemes: []string{"test-deducted"}}
}

func TestRenditionPolicyRejectsAmbiguousSchemes(t *testing.T) {
	for _, schemes := range [][]string{{"test-deducted"}, {" "}, {"monthly", "monthly"}} {
		policy := testRenditionApplyPolicy()
		policy.DeferredFeeSchemes = schemes
		if policy.validate() == nil {
			t.Fatalf("accepted ambiguous policy: %v", schemes)
		}
	}
}

func TestRenditionDeferredFees(t *testing.T) {
	for _, fee := range []string{"0", "100"} {
		t.Run(fee, func(t *testing.T) {
			s, _, objects, report, now := renditionFixture(t, 1)
			policy := testRenditionApplyPolicy()
			policy.DeferredFeeSchemes = []string{"monthly"}
			report.Items[0].FeeScheme = "monthly"
			report.Items[0].Fee = json.Number(fee)
			id, err := s.ReceiveRendition(context.Background(), objects, "dev", report, []byte("signed"), policy, now)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.ProcessRendition(context.Background(), objects, id, now, 50); err != nil {
				t.Fatal(err)
			}
			row, err := s.read(context.Background(), renditionPK(id), "ITEM#"+report.Items[0].PaymentID)
			if err != nil {
				t.Fatal(err)
			}
			want := "FEE_SCHEME_REVIEW"
			if fee == "0" {
				want = "APPLIED"
				if row.RenditionResult.FeeTreatment != "DEFERRED" {
					t.Fatal("monthly fee treated as free")
				}
			}
			if row.RenditionResult.Status != want {
				t.Fatalf("got %s, want %s", row.RenditionResult.Status, want)
			}
		})
	}
}

func TestRendition440PassengersAcrossTenTrips(t *testing.T) {
	s, db, objects, r, now := renditionFixture(t, 440)
	id, err := s.ReceiveRendition(context.Background(), objects, "dev", r, []byte("authenticated-test-evidence"), testRenditionApplyPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	for range 9 {
		if err = s.ProcessRendition(context.Background(), objects, id, now, 50); err != nil {
			t.Fatal(err)
		}
	}
	row, err := s.read(context.Background(), renditionPK(id), "META")
	if err != nil {
		t.Fatal(err)
	}
	if row.Rendition.Status != "COMPLETED" || row.Rendition.Applied != 440 {
		t.Fatalf("%+v", row.Rendition)
	}
	cash := int64(0)
	trips := map[string]bool{}
	for _, item := range r.Items {
		result, e := s.read(context.Background(), renditionPK(id), "ITEM#"+item.PaymentID)
		if e != nil {
			t.Fatal(e)
		}
		trips[result.RenditionResult.TripID] = true
		cash += result.RenditionResult.Gross - result.RenditionResult.Fee
	}
	if cash != 4_356_000 || len(trips) != 10 || db.maxActions > 100 {
		t.Fatalf("cash=%d trips=%d actions=%d", cash, len(trips), db.maxActions)
	}
	commits := db.commits
	if err = s.ProcessRendition(context.Background(), objects, id, now, 50); err != nil {
		t.Fatal(err)
	}
	if db.commits != commits {
		t.Fatal("completed report reapplied")
	}
}

func TestRenditionObserveAndReplay(t *testing.T) {
	s, db, objects, r, now := renditionFixture(t, 2)
	id, err := s.ReceiveRendition(context.Background(), objects, "dev", r, []byte("signed"), RenditionPolicy{Mode: "OBSERVE"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessRendition(context.Background(), objects, id, now, 50); err != nil {
		t.Fatal(err)
	}
	for _, item := range r.Items {
		if _, e := s.read(context.Background(), "COMMAND#"+paymentOperationID("rendition-item:"+id+":"+item.PaymentID), "META"); !errors.Is(e, ErrNotFound) {
			t.Fatal("observation changed cash")
		}
	}
	if err = s.RescheduleRendition(context.Background(), id, renditionJobSK(now, id), now, false); err != nil {
		t.Fatal(err)
	}
	if err = s.ReplayRendition(context.Background(), id, testRenditionApplyPolicy(), "operator", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessRendition(context.Background(), objects, id, now, 50); err != nil {
		t.Fatal(err)
	}
	row, _ := s.read(context.Background(), renditionPK(id), "META")
	if row.Rendition.Applied != 2 {
		t.Fatalf("%+v", row.Rendition)
	}
	if len(db.items) == 0 {
		t.Fatal("no persistence")
	}
}

func TestRenditionCrashAfterCashCommitAndBeforeCheckpoint(t *testing.T) {
	s, db, objects, r, now := renditionFixture(t, 1)
	id, err := s.ReceiveRendition(context.Background(), objects, "dev", r, []byte("signed"), testRenditionApplyPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	db.failOnCommit = db.commits + 2
	if err = s.ProcessRendition(context.Background(), objects, id, now, 50); err == nil {
		t.Fatal("interruption not simulated")
	}
	db.failOnCommit = 0
	if err = s.ProcessRendition(context.Background(), objects, id, now, 50); err != nil {
		t.Fatal(err)
	}
	row, _ := s.read(context.Background(), renditionPK(id), "META")
	if row.Rendition.Applied != 1 {
		t.Fatalf("%+v", row.Rendition)
	}
}

func TestRenditionIdempotencyAndMismatch(t *testing.T) {
	s, _, objects, r, now := renditionFixture(t, 2)
	id, err := s.ReceiveRendition(context.Background(), objects, "dev", r, []byte("signed"), RenditionPolicy{Mode: "OBSERVE"}, now)
	if err != nil {
		t.Fatal(err)
	}
	r.Items[0], r.Items[1] = r.Items[1], r.Items[0]
	r.Items[0].Amount = "10000"
	again, err := s.ReceiveRendition(context.Background(), objects, "dev", r, []byte("resigned"), RenditionPolicy{Mode: "OBSERVE"}, now.Add(time.Minute))
	if err != nil || again != id {
		t.Fatal("equivalent retry", err)
	}
	r.Items[0].Fee = "99"
	if _, err = s.ReceiveRendition(context.Background(), objects, "dev", r, []byte("signed"), RenditionPolicy{Mode: "OBSERVE"}, now); !errors.Is(err, ErrReplayMismatch) {
		t.Fatal("altered report accepted", err)
	}
}

func TestRenditionExceptionsDoNotDuplicateCash(t *testing.T) {
	for _, scenario := range []string{"monthly fee", "manual settlement", "integrator", "amount mismatch", "missing confirmation"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, objects, r, now := renditionFixture(t, 1)
			item := r.Items[0]
			outcome, _ := s.read(context.Background(), "ATTEMPT#"+item.AttemptID, "OUTCOME")
			pk := "TRIP#" + outcome.Event.TripID
			switch scenario {
			case "monthly fee":
				r.Items[0].FeeScheme = "a fin de mes"
			case "integrator":
				r.Items[0].IntegratorFee = "50"
			case "manual settlement":
				if _, _, e := s.ReconcileSettlement(context.Background(), outcome.Event.TripID, "khipu:"+item.PaymentID, 1, 10000, 100, "bank:test", "2026-10-17", treasuryAudit("manual")); e != nil {
					t.Fatal(e)
				}
			case "amount mismatch":
				r.Items[0].Amount = "12000"
			case "missing confirmation":
				delete(db.items, "ATTEMPT#"+item.AttemptID+"/OUTCOME")
			}
			id, e := s.ReceiveRendition(context.Background(), objects, "dev", r, []byte("signed"), testRenditionApplyPolicy(), now)
			if e != nil {
				t.Fatal(e)
			}
			e = s.ProcessRendition(context.Background(), objects, id, now, 50)
			if scenario == "missing confirmation" {
				if !errors.Is(e, ErrNotFound) {
					t.Fatal(e)
				}
			} else if e != nil {
				t.Fatal(e)
			}
			state, _ := s.read(context.Background(), pk, "SETTLEMENT#khipu:"+item.PaymentID)
			want := int64(0)
			if scenario == "manual settlement" {
				want = 10000
			}
			if state.Settlement.SettledGross != want {
				t.Fatal("unexpected cash change")
			}
		})
	}
}

func TestRenditionStorageFailureAndTampering(t *testing.T) {
	s, _, objects, r, now := renditionFixture(t, 1)
	objects.fail = true
	if _, err := s.ReceiveRendition(context.Background(), objects, "dev", r, []byte("signed"), RenditionPolicy{Mode: "OBSERVE"}, now); err == nil {
		t.Fatal("storage failure acknowledged")
	}
	objects.fail = false
	id, err := s.ReceiveRendition(context.Background(), objects, "dev", r, []byte("signed"), RenditionPolicy{Mode: "OBSERVE"}, now)
	if err != nil {
		t.Fatal(err)
	}
	row, _ := s.read(context.Background(), renditionPK(id), "META")
	objects.values[row.Rendition.ObjectKey] = []byte("tampered")
	if err = s.ProcessRendition(context.Background(), objects, id, now, 50); !errors.Is(err, ErrReplayMismatch) {
		t.Fatal(err)
	}
}

func TestRenditionConfigDefaultsAndActivationGuard(t *testing.T) {
	c, err := decodeRenditionConfig([]byte(`{"recipientUid":"merchant","bank":"test","account":"test"}`))
	if err != nil || c.Policy.Mode != "OBSERVE" {
		t.Fatal(err)
	}
	b, err := json.Marshal(renditionConfig{RecipientUID: "merchant", Bank: "test", Account: "test", Policy: RenditionPolicy{Mode: "APPLY"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decodeRenditionConfig(b); err == nil {
		t.Fatal("unsafe activation accepted")
	}
}

func TestRenditionWorkerDurableRetriesAndKillSwitch(t *testing.T) {
	s, db, objects, r, now := renditionFixture(t, 1)
	policy := testRenditionApplyPolicy()
	id, err := s.ReceiveRendition(context.Background(), objects, "dev", r, []byte("signed"), policy, now)
	if err != nil {
		t.Fatal(err)
	}
	clock := now.Add(time.Second)
	a := RenditionApp{Accounts: s, Objects: objects, Policy: RenditionPolicy{Mode: "OBSERVE"}, Now: func() time.Time { return clock }}
	if err = a.Run(context.Background()); err == nil {
		t.Fatal("revoked policy must be visible as worker failure")
	}
	row, err := s.read(context.Background(), renditionPK(id), "META")
	if err != nil {
		t.Fatal(err)
	}
	if row.Rendition.Cursor != 0 || row.Rendition.Retries != 1 {
		t.Fatal("kill switch ignored")
	}
	if _, err = s.read(context.Background(), "RENDITION_JOBS", renditionJobSK(clock.Add(5*time.Minute), id)); err != nil {
		t.Fatal("retry lost", err)
	}
	a.Policy = policy
	clock = clock.Add(6 * time.Minute)
	if err = a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	row, err = s.read(context.Background(), renditionPK(id), "META")
	if err != nil {
		t.Fatal(err)
	}
	if row.Rendition.Status != "COMPLETED" {
		t.Fatalf("%+v", row.Rendition)
	}
	for k := range db.items {
		if strings.HasPrefix(k, "RENDITION_JOBS/") {
			t.Fatal("completed job retained")
		}
	}
}

func TestRenditionBeforeConfirmationRecovers(t *testing.T) {
	s, db, objects, r, now := renditionFixture(t, 1)
	outcome, err := s.read(context.Background(), "ATTEMPT#"+r.Items[0].AttemptID, "OUTCOME")
	if err != nil {
		t.Fatal(err)
	}
	delete(db.items, outcome.PK+"/"+outcome.SK)
	id, err := s.ReceiveRendition(context.Background(), objects, "dev", r, []byte("signed"), testRenditionApplyPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	clock := now.Add(time.Second)
	a := RenditionApp{Accounts: s, Objects: objects, Policy: testRenditionApplyPolicy(), Now: func() time.Time { return clock }}
	if err = a.Run(context.Background()); err == nil {
		t.Fatal("missing confirmation must be visible as worker failure")
	}
	saveLookupRow(t, db, outcome)
	clock = clock.Add(6 * time.Minute)
	if err = a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	row, err := s.read(context.Background(), renditionPK(id), "META")
	if err != nil {
		t.Fatal(err)
	}
	if row.Rendition.Applied != 1 {
		t.Fatalf("%+v", row.Rendition)
	}
}

func TestRenditionAdminMonthlyList(t *testing.T) {
	for _, count := range []int{0, 1, 51} {
		t.Run(fmt.Sprintf("reports_%d", count), func(t *testing.T) {
			s, db := serviceFixture(t)
			for i := 0; i < count; i++ {
				id := paymentOperationID(fmt.Sprintf("monthly-report-%d", i))
				saveLookupRow(t, db, record{PK: "RENDITION_MONTH#2026-10", SK: id, Rendition: &RenditionState{ID: id, Status: "OBSERVED"}})
			}
			// El listado debe permanecer aislado al mes solicitado.
			saveLookupRow(t, db, record{PK: "RENDITION_MONTH#2026-09", SK: approvedID, Rendition: &RenditionState{ID: approvedID}})
			a := RenditionApp{Accounts: s}
			req := adminRequest("GET", "")
			req.PathParameters = nil
			req.QueryStringParameters = map[string]string{"month": "2026-10"}
			seen := map[string]bool{}
			for pageNumber := 0; ; pageNumber++ {
				if pageNumber > 1 {
					t.Fatal("pagination did not terminate")
				}
				response, err := a.HandleAdmin(context.Background(), req)
				if err != nil || response.StatusCode != 200 {
					t.Fatalf("monthly list: status=%d err=%v", response.StatusCode, err)
				}
				var page struct {
					Data struct {
						Items []RenditionState `json:"items"`
						Next  string           `json:"nextCursor"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(response.Body), &page); err != nil {
					t.Fatal(err)
				}
				if len(page.Data.Items) > 50 {
					t.Fatal("unbounded page")
				}
				for _, item := range page.Data.Items {
					if seen[item.ID] || item.Status != "OBSERVED" {
						t.Fatal("duplicate report or report from another month")
					}
					seen[item.ID] = true
				}
				if page.Data.Next == "" {
					break
				}
				req.QueryStringParameters["cursor"] = page.Data.Next
			}
			if len(seen) != count {
				t.Fatalf("got %d reports, want %d", len(seen), count)
			}
		})
	}
}

func TestRenditionAdminPaginationAndPrivacy(t *testing.T) {
	s, _, objects, r, now := renditionFixture(t, 51)
	id, err := s.ReceiveRendition(context.Background(), objects, "dev", r, []byte("signed"), RenditionPolicy{Mode: "OBSERVE"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = s.ProcessRendition(context.Background(), objects, id, now, 50); err != nil {
			t.Fatal(err)
		}
	}
	a := RenditionApp{Accounts: s, Objects: objects, Policy: RenditionPolicy{Mode: "OBSERVE"}, Now: func() time.Time { return now }}
	req := adminRequest("GET", "")
	req.PathParameters = map[string]string{"id": id}
	resp, err := a.HandleAdmin(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || strings.Contains(resp.Body, "test-account") || strings.Contains(resp.Body, ".jws.gz") {
		t.Fatal("invalid or private response", resp.StatusCode)
	}
	var page struct {
		Data struct {
			Items []RenditionResult `json:"items"`
			Next  string            `json:"nextCursor"`
		} `json:"data"`
	}
	if err = json.Unmarshal([]byte(resp.Body), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data.Items) != 50 || page.Data.Next == "" {
		t.Fatal("missing bounded page")
	}
	req.QueryStringParameters = map[string]string{"cursor": page.Data.Next}
	resp, err = a.HandleAdmin(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(resp.Body), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data.Items) != 1 {
		t.Fatal("missing second page")
	}
	req.RequestContext.Authorizer = nil
	resp, err = a.HandleAdmin(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 && resp.StatusCode != 401 {
		t.Fatal("unauthorized access")
	}
}

func TestRenditionPublicHandlerRejectsInvalidEvidence(t *testing.T) {
	a := RenditionApp{Objects: &renditionMemoryObjects{values: map[string][]byte{}}, Now: time.Now, Verifier: providers.RenditionVerifier{}}
	for _, tc := range []struct {
		body   string
		base64 bool
		want   int
	}{{"{}", false, 400}, {"invalid!", true, 400}, {strings.Repeat("x", providers.MaxRenditionBytes*4/3+9), false, 413}} {
		req := events.APIGatewayV2HTTPRequest{Body: tc.body, IsBase64Encoded: tc.base64}
		req.RequestContext.HTTP.Method = "POST"
		if got := a.Handle(context.Background(), req); got.StatusCode != tc.want {
			t.Fatalf("status=%d", got.StatusCode)
		}
	}
}
