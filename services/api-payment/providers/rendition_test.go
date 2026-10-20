package providers

import (
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

func renditionTestReport() Rendition {
	return Rendition{ID: 1, RecipientUID: "merchant", Bank: "test-bank", Account: "test-account", StartDate: "2026-10-17T00:00:00Z", EndDate: "2026-10-17T12:00:00Z", Items: []RenditionItem{{PaymentID: "abcdefghijkl", AttemptID: "attempt", PaymentDate: "2026-10-17T10:00:00Z", Amount: "10000", Fee: "100", FeeScheme: "test"}}}
}

func TestRenditionSignatureAndTrust(t *testing.T) {
	now := time.Date(2026, 10, 17, 14, 0, 0, 0, time.UTC)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	issue := func(host string) *x509.Certificate {
		t.Helper()
		leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{host}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
		der, e := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, key)
		if e != nil {
			t.Fatal(e)
		}
		cert, e := x509.ParseCertificate(der)
		if e != nil {
			t.Fatal(e)
		}
		return cert
	}
	cert, wrongHost := issue("khipu.com"), issue("other.example")
	sign := func(c *x509.Certificate, alg string, report Rendition, iat int64, chain bool) []byte {
		t.Helper()
		h := map[string]any{"alg": alg}
		if chain {
			h["x5c"] = []string{base64.StdEncoding.EncodeToString(c.Raw)}
		}
		hb, e := json.Marshal(h)
		if e != nil {
			t.Fatal(e)
		}
		pb, e := json.Marshal(map[string]any{"iat": iat, "report": report})
		if e != nil {
			t.Fatal(e)
		}
		text := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
		d := sha512.Sum512([]byte(text))
		sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA512, d[:])
		if e != nil {
			t.Fatal(e)
		}
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		if _, e = gz.Write([]byte(text + "." + base64.RawURLEncoding.EncodeToString(sig))); e != nil {
			t.Fatal(e)
		}
		if e = gz.Close(); e != nil {
			t.Fatal(e)
		}
		return buf.Bytes()
	}
	valid := sign(cert, "RS512", renditionTestReport(), now.Unix(), true)
	for _, tc := range []struct {
		name, stage, account string
		cert                 *x509.Certificate
		algorithm            string
		at                   time.Time
		valid                bool
	}{
		{"expired arbitrary certificate", "dev", "382670112717", wrongHost, "RS512", now.Add(365 * 24 * time.Hour), true},
		{"production rejected", "prd", "382670112717", wrongHost, "RS512", now, false},
		{"other account rejected", "dev", "000000000000", wrongHost, "RS512", now, false},
		{"invalid algorithm rejected", "dev", "382670112717", wrongHost, "HS512", now, false},
	} {
		t.Run("development/"+tc.name, func(t *testing.T) {
			v := DevelopmentRenditionVerifier{RenditionVerifier: RenditionVerifier{RecipientUID: "merchant", Bank: "test-bank", Account: "test-account"}, Stage: tc.stage, AWSAccount: tc.account}
			_, err := v.Verify(sign(tc.cert, tc.algorithm, renditionTestReport(), now.Unix(), true), tc.at)
			if (err == nil) != tc.valid {
				t.Fatal("unexpected development verification result", err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*RenditionVerifier, *[]byte, *time.Time)
		want   bool
	}{
		{"valid", func(*RenditionVerifier, *[]byte, *time.Time) {}, true},
		{"untrusted root", func(v *RenditionVerifier, _ *[]byte, _ *time.Time) { v.Roots = x509.NewCertPool() }, false},
		{"wrong hostname", func(_ *RenditionVerifier, b *[]byte, _ *time.Time) {
			*b = sign(wrongHost, "RS512", renditionTestReport(), now.Unix(), true)
		}, false},
		{"expired", func(_ *RenditionVerifier, _ *[]byte, n *time.Time) { *n = now.Add(2 * time.Hour) }, false},
		{"wrong algorithm", func(_ *RenditionVerifier, b *[]byte, _ *time.Time) {
			*b = sign(cert, "HS512", renditionTestReport(), now.Unix(), true)
		}, false},
		{"wrong destination", func(v *RenditionVerifier, _ *[]byte, _ *time.Time) { v.Account = "other" }, false},
		{"future timestamp", func(_ *RenditionVerifier, b *[]byte, _ *time.Time) {
			*b = sign(cert, "RS512", renditionTestReport(), now.Add(time.Hour).Unix(), true)
		}, false},
		{"old replay allowed", func(_ *RenditionVerifier, b *[]byte, _ *time.Time) {
			*b = sign(cert, "RS512", renditionTestReport(), now.Add(-72*time.Hour).Unix(), true)
		}, true},
		{"pinned fallback", func(v *RenditionVerifier, b *[]byte, _ *time.Time) {
			v.Pinned = cert
			*b = sign(cert, "RS512", renditionTestReport(), now.Unix(), false)
		}, true},
		{"missing trusted key", func(_ *RenditionVerifier, b *[]byte, _ *time.Time) {
			*b = sign(cert, "RS512", renditionTestReport(), now.Unix(), false)
		}, false},
		{"tampered gzip", func(_ *RenditionVerifier, b *[]byte, _ *time.Time) { (*b)[len(*b)-1] ^= 1 }, false},
		{"invalid payload", func(_ *RenditionVerifier, b *[]byte, _ *time.Time) {
			r := renditionTestReport()
			r.Items[0].Amount = "100.5"
			*b = sign(cert, "RS512", r, now.Unix(), true)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := RenditionVerifier{Roots: roots, RecipientUID: "merchant", Bank: "test-bank", Account: "test-account"}
			b := append([]byte(nil), valid...)
			n := now
			tc.mutate(&v, &b, &n)
			_, e := v.Verify(b, n)
			if (e == nil) != tc.want {
				t.Fatalf("valid=%v err=%v", tc.want, e)
			}
			capture := RenditionCaptureVerifier{Roots: v.Roots, Pinned: v.Pinned}
			_, captureErr := capture.Verify(b, n)
			// Únicamente la comparación del destino se omite en diagnóstico.
			wantCapture := tc.want || tc.name == "wrong destination"
			if (captureErr == nil) != wantCapture {
				t.Fatalf("capture valid=%v err=%v", wantCapture, captureErr)
			}
		})
	}
	var huge bytes.Buffer
	gz := gzip.NewWriter(&huge)
	if _, err = gz.Write([]byte(strings.Repeat("x", MaxRenditionBytes+1))); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	v := RenditionVerifier{Roots: roots, RecipientUID: "merchant", Bank: "test-bank", Account: "test-account"}
	if _, err = (RenditionVerifier{Roots: roots}).Verify(valid, now); err == nil {
		t.Fatal("normal verification accepted an unknown destination")
	}
	if _, err = v.Verify(huge.Bytes(), now); err == nil {
		t.Fatal("gzip expansion accepted")
	}
	if _, err = (RenditionCaptureVerifier{Roots: roots}).Verify(huge.Bytes(), now); err == nil {
		t.Fatal("capture accepted gzip expansion")
	}
}

func TestRenditionExactAmountsAndDates(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{{"100.0000", true}, {"100/1", false}, {"0x64", false}, {"1e2", true}, {"100.1", false}, {"-1", false}, {"", false}, {"999999999999999999999999999999999999", false}} {
		t.Run(tc.value, func(t *testing.T) {
			_, err := CLPInteger(json.Number(tc.value))
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
	if _, err := RenditionDate("2018-05-15T03:48:00.520+0000"); err != nil {
		t.Fatal(err)
	}
	r := renditionTestReport()
	r.Items = append(r.Items, r.Items[0])
	if r.Validate() == nil {
		t.Fatal("duplicate item accepted")
	}
}
