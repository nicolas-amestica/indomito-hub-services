package providers

import (
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strings"
	"time"
)

// MaxRenditionBytes limita también la expansión gzip de entradas públicas.
const MaxRenditionBytes = 4 * 1024 * 1024

// Rendition conserva únicamente los datos necesarios para el cruce financiero.
type Rendition struct {
	ID           int64           `json:"drnId"`
	RecipientUID string          `json:"recipientUid"`
	Bank         string          `json:"recipientBankName"`
	Account      string          `json:"recipientBankAccountNumber"`
	StartDate    string          `json:"startDate"`
	EndDate      string          `json:"endDate"`
	Items        []RenditionItem `json:"items"`
}

// RenditionItem usa decimales textuales para evitar redondeos binarios de dinero.
type RenditionItem struct {
	PaymentID     string      `json:"khOperationCodeUID"`
	AttemptID     string      `json:"merchantTxID"`
	PaymentDate   string      `json:"paymentDate"`
	FeeScheme     string      `json:"feeScheme"`
	Amount        json.Number `json:"txAmount"`
	Fee           json.Number `json:"khipuFee"`
	IntegratorFee json.Number `json:"integratorFee"`
}

// RenditionVerifier autentica un aviso; el certificado fijo se aprovisiona fuera del mensaje.
type RenditionVerifier struct {
	Roots        *x509.CertPool
	Pinned       *x509.Certificate
	RecipientUID string
	Bank         string
	Account      string
}

// CLPInteger exige un monto entero y acotado sin truncar decimales.
func CLPInteger(value json.Number) (int64, error) {
	if len(value) == 0 || len(value) > 32 || !json.Valid([]byte(value)) || value[0] < '0' || value[0] > '9' {
		return 0, ErrVerification
	}
	r, ok := new(big.Rat).SetString(string(value))
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		return 0, ErrVerification
	}
	n := r.Num().Int64()
	if n < 0 || n > 1_000_000_000_000 {
		return 0, ErrVerification
	}
	return n, nil
}

// RenditionDate admite ISO-8601 y el offset sin dos puntos del ejemplo oficial.
func RenditionDate(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999-0700"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, nil
		}
	}
	return time.Time{}, ErrVerification
}

// Verify valida el sobre firmado y la cuenta destino antes de devolver datos.
// Un aviso antiguo válido puede recuperarse; la deduplicación usa drnId, no iat.
func (v RenditionVerifier) Verify(raw []byte, now time.Time) (Rendition, error) {
	var empty Rendition
	if len(raw) == 0 || len(raw) > MaxRenditionBytes || now.IsZero() || v.RecipientUID == "" || v.Bank == "" || v.Account == "" {
		return empty, ErrVerification
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return empty, ErrVerification
	}
	b, readErr := io.ReadAll(io.LimitReader(gz, MaxRenditionBytes+1))
	closeErr := gz.Close()
	if readErr != nil || closeErr != nil || len(b) > MaxRenditionBytes {
		return empty, ErrVerification
	}
	parts := strings.Split(string(b), ".")
	if len(parts) != 3 {
		return empty, ErrVerification
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(header) > 64*1024 {
		return empty, ErrVerification
	}
	var h struct {
		Alg   string   `json:"alg"`
		Chain []string `json:"x5c"`
		Crit  []string `json:"crit"`
		B64   *bool    `json:"b64"`
	}
	if json.Unmarshal(header, &h) != nil || h.Alg != "RS512" || len(h.Crit) != 0 || (h.B64 != nil && !*h.B64) || len(h.Chain) > 8 {
		return empty, ErrVerification
	}
	cert := v.Pinned
	if len(h.Chain) > 0 {
		intermediates := x509.NewCertPool()
		for i, encoded := range h.Chain {
			der, e := base64.StdEncoding.DecodeString(encoded)
			if e != nil {
				return empty, ErrVerification
			}
			c, e := x509.ParseCertificate(der)
			if e != nil {
				return empty, ErrVerification
			}
			if i == 0 {
				cert = c
			} else {
				intermediates.AddCert(c)
			}
		}
		// Khipu prescribe Any EKU para este certificado de firma, no ServerAuth.
		if _, err = cert.Verify(x509.VerifyOptions{Roots: v.Roots, Intermediates: intermediates, DNSName: "khipu.com", CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
			return empty, ErrVerification
		}
	}
	if cert == nil || now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return empty, ErrVerification
	}
	key, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || key.N.BitLen() < 2048 {
		return empty, ErrVerification
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return empty, ErrVerification
	}
	digest := sha512.Sum512([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA512, digest[:], sig) != nil {
		return empty, ErrVerification
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return empty, ErrVerification
	}
	var envelope struct {
		Issued json.Number `json:"iat"`
		Report Rendition   `json:"report"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return empty, ErrVerification
	}
	iat, err := envelope.Issued.Int64()
	if err != nil || iat <= 0 || iat > now.Add(5*time.Minute).Unix() {
		return empty, ErrVerification
	}
	r := envelope.Report
	if r.RecipientUID != v.RecipientUID || r.Bank != v.Bank || r.Account != v.Account {
		return empty, ErrVerification
	}
	if err = r.Validate(); err != nil {
		return empty, err
	}
	return r, nil
}

// Validate rechaza reportes ambiguos antes de persistir o procesar algún pago.
func (r Rendition) Validate() error {
	start, e1 := RenditionDate(r.StartDate)
	end, e2 := RenditionDate(r.EndDate)
	if r.ID <= 0 || r.RecipientUID == "" || r.Account == "" || r.Bank == "" || e1 != nil || e2 != nil || end.Before(start) || len(r.Items) == 0 || len(r.Items) > 10000 {
		return ErrVerification
	}
	seen := map[string]bool{}
	for _, i := range r.Items {
		if len(i.PaymentID) != 12 || i.AttemptID == "" || len(i.AttemptID) > 128 || len(i.FeeScheme) > 256 || seen[i.PaymentID] {
			return ErrVerification
		}
		for _, c := range i.PaymentID {
			alphaNumeric := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
			if !alphaNumeric {
				return ErrVerification
			}
		}
		seen[i.PaymentID] = true
		amount, aerr := CLPInteger(i.Amount)
		fee, ferr := CLPInteger(i.Fee)
		if aerr != nil || ferr != nil || amount <= 0 || fee > amount {
			return ErrVerification
		}
		if i.IntegratorFee != "" {
			n, err := CLPInteger(i.IntegratorFee)
			if err != nil || n > amount-fee {
				return ErrVerification
			}
		}
		if _, err := RenditionDate(i.PaymentDate); err != nil {
			return ErrVerification
		}
	}
	return nil
}

// ErrRenditionPolicy diferencia datos auténticos de reglas de aplicación aún no validadas.
var ErrRenditionPolicy = errors.New("politica de rendicion no validada")
