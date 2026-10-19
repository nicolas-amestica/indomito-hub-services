package renditionobserve

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type fixture struct {
	p       Policy
	raw     []byte
	cert    *x509.Certificate
	now     time.Time
	key     *rsa.PrivateKey
	payload map[string]any
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "expired-dev-fixture"}, NotBefore: now.Add(-72 * time.Hour), NotAfter: now.Add(-24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	items := []any{}
	for i := range 3 {
		items = append(items, map[string]any{"khOperationCodeUID": fmt.Sprintf("testpay%05d", i), "merchantTxID": fmt.Sprintf("attempt-%d", i), "paymentDate": now.Add(-time.Hour).Format(time.RFC3339), "fundsRenditionDate": now.Add(-time.Hour).Format(time.RFC3339), "txAmount": 10000, "khipuFee": 0, "khipuDelayedFee": 0, "feeScheme": "developer-test"})
	}
	f := fixture{now: now, key: key, cert: cert, p: Policy{Stage: "dev", Mode: "OBSERVE", ApprovedAt: now.Add(-time.Minute), Until: now.Add(time.Hour), CertificateSHA256: digest(der), RecipientUID: "dev-merchant", Bank: "demo", Account: "test-account", FeeScheme: "developer-test"}, payload: map[string]any{"iat": now.Unix(), "report": map[string]any{"drnId": 1, "recipientUid": "dev-merchant", "recipientBankName": "demo", "recipientBankAccountNumber": "test-account", "startDate": now.Add(-time.Hour).Format(time.RFC3339), "endDate": now.Format(time.RFC3339), "items": items}}}
	f.sign(t, "RS512", false)
	return f
}

func (f *fixture) sign(t *testing.T, alg string, badSignature bool) {
	t.Helper()
	h, err := json.Marshal(map[string]any{"alg": alg, "x5c": []string{base64.StdEncoding.EncodeToString(f.cert.Raw)}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := json.Marshal(f.payload)
	if err != nil {
		t.Fatal(err)
	}
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	sum := sha512.Sum512([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA512, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	if badSignature {
		sig[0] ^= 1
	}
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if _, err = gz.Write([]byte(input + "." + base64.RawURLEncoding.EncodeToString(sig))); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.raw = b.Bytes()
	f.p.ReportSHA256 = digest(f.raw)
}

type memoryReader struct {
	calls    int
	scenario string
}

func (r *memoryReader) Read(_ context.Context, attempt, payment string) (Snapshot, error) {
	r.calls++
	s := Snapshot{Attempt: &domain.Attempt{ID: attempt, AccountID: "account", Amount: 10000}, Account: &domain.Account{ID: "account", TripID: "trip", ParticipantID: "participant"}, Event: &domain.Event{Type: "PAYMENT_RECEIVED", Amount: 10000, AccountID: "account", TripID: "trip", Reference: "khipu:" + payment}, Settlement: &domain.Settlement{TripID: "trip", PaymentReference: "khipu:" + payment, Amount: 10000}}
	switch r.scenario {
	case "missing":
		s.Attempt = nil
	case "amount":
		s.Event.Amount = 9999
	case "manual":
		s.Settlement.SettledGross = 10000
	case "resolved":
		s.Resolved = true
	case "error":
		return Snapshot{}, errors.New("unavailable")
	}
	return s, nil
}

type memoryWriter struct {
	data   map[string][]byte
	writes int
}

func (w *memoryWriter) SaveOnce(_ context.Context, key string, b []byte) (bool, error) {
	if old, ok := w.data[key]; ok {
		if !bytes.Equal(old, b) {
			return false, errors.New("mismatch")
		}
		return true, nil
	}
	w.data[key] = b
	w.writes++
	return false, nil
}

func TestObserveDEVExpiryExceptionIsLimitedAndIdempotent(t *testing.T) {
	f := newFixture(t)
	reader := &memoryReader{}
	writer := &memoryWriter{data: map[string][]byte{}}
	for i := range 2 {
		r, err := Observe(context.Background(), "dev", DevAccount, f.p, f.raw, f.now, reader, writer)
		if err != nil {
			t.Fatal(err)
		}
		if r.Trust != TrustLabel || r.ProviderAuthenticated || !r.SignatureValid || r.Mode != "OBSERVE" || r.CashWrites != 0 || r.Matched != 3 || r.Gross != 30000 || r.Duplicate != (i == 1) {
			t.Fatalf("unexpected summary %+v", r)
		}
	}
	if writer.writes != 1 {
		t.Fatal("duplicate observation persisted")
	}
	strict := providers.RenditionVerifier{Pinned: f.cert, RecipientUID: f.p.RecipientUID, Bank: f.p.Bank, Account: f.p.Account}
	if _, err := strict.Verify(f.raw, f.now); err == nil {
		t.Fatal("strict verifier accepted expired certificate")
	}
}

func TestObserveRejectsOutsideAuthorization(t *testing.T) {
	f := newFixture(t)
	for _, scenario := range []string{"prd", "account", "policy-stage", "apply", "expired", "future-approval", "long-window", "wrong-cert", "wrong-report", "tampered", "signature", "algorithm", "destination", "future-iat", "date", "delayed-fee"} {
		t.Run(scenario, func(t *testing.T) {
			local := f
			local.p = f.p
			local.raw = append([]byte(nil), f.raw...)
			payload, err := json.Marshal(f.payload)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(payload, &local.payload); err != nil {
				t.Fatal(err)
			}
			stage, account := "dev", DevAccount
			report := local.payload["report"].(map[string]any)
			switch scenario {
			case "prd":
				stage = "prd"
			case "account":
				account = "other"
			case "policy-stage":
				local.p.Stage = "prd"
			case "apply":
				local.p.Mode = "APPLY"
			case "expired":
				local.p.Until = f.now
			case "future-approval":
				local.p.ApprovedAt = f.now.Add(time.Minute)
			case "long-window":
				local.p.Until = f.now.Add(73 * time.Hour)
			case "wrong-cert":
				local.p.CertificateSHA256 = digest([]byte("different certificate"))
			case "wrong-report":
				local.p.ReportSHA256 = digest([]byte("different report"))
			case "tampered":
				local.raw[0] ^= 1
			case "signature":
				local.sign(t, "RS512", true)
			case "algorithm":
				local.sign(t, "none", false)
			case "destination":
				report["recipientUid"] = "other"
				local.sign(t, "RS512", false)
			case "future-iat":
				local.payload["iat"] = f.now.Add(time.Hour).Unix()
				local.sign(t, "RS512", false)
			case "date":
				report["items"].([]any)[0].(map[string]any)["fundsRenditionDate"] = "invalid"
				local.sign(t, "RS512", false)
			case "delayed-fee":
				report["items"].([]any)[0].(map[string]any)["khipuDelayedFee"] = -1
				local.sign(t, "RS512", false)
			}
			reader := &memoryReader{}
			writer := &memoryWriter{data: map[string][]byte{}}
			if _, err = Observe(context.Background(), stage, account, local.p, local.raw, f.now, reader, writer); err == nil {
				t.Fatal("unsafe exception accepted")
			}
			if reader.calls != 0 || writer.writes != 0 {
				t.Fatal("side effect before verification")
			}
		})
	}
}

func TestObserveMismatchesRemainReview(t *testing.T) {
	f := newFixture(t)
	for _, scenario := range []string{"missing", "amount", "manual", "resolved", "error"} {
		t.Run(scenario, func(t *testing.T) {
			writer := &memoryWriter{data: map[string][]byte{}}
			r, err := Observe(context.Background(), "dev", DevAccount, f.p, f.raw, f.now, &memoryReader{scenario: scenario}, writer)
			if scenario == "error" {
				if err == nil || writer.writes != 0 {
					t.Fatal("storage failure ignored")
				}
				return
			}
			if err != nil || r.Review != 3 || r.Matched != 0 || r.CashWrites != 0 {
				t.Fatal(r, err)
			}
		})
	}
}

func TestObserveRejectsMalformedEnvelopeAndMissingDependencies(t *testing.T) {
	f := newFixture(t)
	if _, err := Observe(context.Background(), "dev", DevAccount, f.p, f.raw, f.now, nil, nil); err == nil {
		t.Fatal("nil dependencies accepted")
	}
	for _, raw := range [][]byte{nil, []byte("not gzip"), make([]byte, providers.MaxRenditionBytes+1)} {
		p := f.p
		p.ReportSHA256 = digest(raw)
		if _, _, err := verify(p, raw, f.now); err == nil {
			t.Fatal("invalid body accepted")
		}
	}
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if _, err := gz.Write([]byte("not-a-jws")); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.p.ReportSHA256 = digest(b.Bytes())
	if _, _, err := verify(f.p, b.Bytes(), f.now); err == nil {
		t.Fatal("invalid compact accepted")
	}
}
