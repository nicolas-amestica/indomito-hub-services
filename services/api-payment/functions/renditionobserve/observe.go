// Package renditionobserve permite observar una muestra autorizada exclusivamente
// en DEV. No altera el verificador de producción ni ofrece operaciones de caja.
package renditionobserve

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

// DevAccount impide usar la excepción en otra cuenta AWS, incluso con stage dev.
const DevAccount = "382670112717"

// TrustLabel nunca equivale a autenticidad del proveedor.
const TrustLabel = "DEV_PINNED_CERT_EXCEPTION_UNTRUSTED"

// Policy fija una única evidencia y certificado por un máximo de 72 horas.
type Policy struct {
	Stage             string    `json:"stage"`
	Mode              string    `json:"mode"`
	ApprovedAt        time.Time `json:"approvedAt"`
	Until             time.Time `json:"until"`
	CertificateSHA256 string    `json:"certificateSHA256"`
	ReportSHA256      string    `json:"reportSHA256"`
	RecipientUID      string    `json:"recipientUid"`
	Bank              string    `json:"bank"`
	Account           string    `json:"account"`
	FeeScheme         string    `json:"feeScheme"`
}

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (p Policy) validate(stage, account string, now time.Time) error {
	if stage != "dev" || account != DevAccount || p.Stage != "dev" || p.Mode != "OBSERVE" || now.IsZero() || p.ApprovedAt.IsZero() || now.Before(p.ApprovedAt) || !now.Before(p.Until) || p.Until.Sub(p.ApprovedAt) > 72*time.Hour || p.RecipientUID == "" || p.Bank == "" || p.Account == "" || p.FeeScheme == "" || !hashPattern.MatchString(p.CertificateSHA256) || !hashPattern.MatchString(p.ReportSHA256) {
		return errors.New("excepción DEV no habilitada")
	}
	return nil
}

// Snapshot contiene únicamente los datos financieros necesarios para el cruce.
type Snapshot struct {
	Attempt    *domain.Attempt
	Account    *domain.Account
	Event      *domain.Event
	Settlement *domain.Settlement
	Resolved   bool
}

// Reader no permite escrituras ni scans.
type Reader interface {
	Read(context.Context, string, string) (Snapshot, error)
}

// Writer conserva solo resúmenes diagnósticos, nunca asientos. Retorna duplicado.
type Writer interface {
	SaveOnce(context.Context, string, []byte) (bool, error)
}

// ItemResult omite identificadores personales, cuentas bancarias y cuerpos crudos.
type ItemResult struct {
	Index              int    `json:"index"`
	Status             string `json:"status"`
	Gross              int64  `json:"gross"`
	Fee                int64  `json:"immediateFee"`
	DelayedFee         int64  `json:"delayedFee"`
	FeeScheme          string `json:"feeScheme"`
	PaymentDate        string `json:"paymentDate"`
	FundsRenditionDate string `json:"fundsRenditionDate"`
	SettledGross       int64  `json:"settledGross"`
}

// Result distingue la firma matemática de la confianza en Khipu y la caja.
type Result struct {
	Trust                 string       `json:"trust"`
	Mode                  string       `json:"mode"`
	SignatureValid        bool         `json:"signatureValid"`
	ProviderAuthenticated bool         `json:"providerAuthenticated"`
	DestinationCheck      string       `json:"destinationCheck"`
	ExceptionUntil        time.Time    `json:"exceptionUntil"`
	ReportSHA256          string       `json:"reportSHA256"`
	Matched               int          `json:"matched"`
	Review                int          `json:"review"`
	Gross                 int64        `json:"gross"`
	CashWrites            int          `json:"cashWrites"`
	Duplicate             bool         `json:"duplicate"`
	StartDate             string       `json:"startDate"`
	EndDate               string       `json:"endDate"`
	Items                 []ItemResult `json:"items"`
}

type extraItem struct {
	DelayedFee json.Number `json:"khipuDelayedFee"`
	FundsDate  string      `json:"fundsRenditionDate"`
}

// Observe verifica pin, firma y datos, cruza por GetItem y guarda un resumen.
// La excepción solo relaja confianza X.509 para el certificado fijado; no firma,
// destinatario, importe, ambiente, fecha de autorización ni hash de la muestra.
func Observe(ctx context.Context, stage, account string, p Policy, raw []byte, now time.Time, reader Reader, writer Writer) (Result, error) {
	var result Result
	if err := p.validate(stage, account, now); err != nil {
		return result, err
	}
	if reader == nil || writer == nil {
		return result, errors.New("dependencias incompletas")
	}
	report, extra, err := verify(p, raw, now)
	if err != nil {
		return result, err
	}
	result = Result{Trust: TrustLabel, Mode: "OBSERVE", SignatureValid: true, DestinationCheck: "MATCHES_APPROVED_DEV_SAMPLE_NOT_BANK_VERIFIED", ExceptionUntil: p.Until, ReportSHA256: p.ReportSHA256, StartDate: report.StartDate, EndDate: report.EndDate}
	for idx, item := range report.Items {
		gross, e := providers.CLPInteger(item.Amount)
		if e != nil {
			return Result{}, e
		}
		fee, e := providers.CLPInteger(item.Fee)
		if e != nil {
			return Result{}, e
		}
		delayed, e := providers.CLPInteger(extra[idx].DelayedFee)
		if e != nil {
			return Result{}, e
		}
		s, e := reader.Read(ctx, item.AttemptID, item.PaymentID)
		if e != nil {
			return Result{}, e
		}
		r := ItemResult{Index: idx + 1, Status: "REVIEW", Gross: gross, Fee: fee, DelayedFee: delayed, FeeScheme: item.FeeScheme, PaymentDate: item.PaymentDate, FundsRenditionDate: extra[idx].FundsDate}
		if s.Settlement != nil {
			r.SettledGross = s.Settlement.SettledGross
		}
		if s.Attempt != nil && s.Account != nil && s.Event != nil && s.Settlement != nil && !s.Resolved && s.Attempt.ID == item.AttemptID && s.Attempt.AccountID == s.Account.ID && s.Account.ParticipantID != "" && s.Account.TripID != "" && s.Event.AccountID == s.Account.ID && s.Event.TripID == s.Account.TripID && s.Event.Type == "PAYMENT_RECEIVED" && s.Event.Reference == "khipu:"+item.PaymentID && s.Event.Amount == gross && s.Attempt.Amount == gross && s.Settlement.Amount == gross && s.Settlement.TripID == s.Account.TripID && s.Settlement.PaymentReference == s.Event.Reference && s.Settlement.SettledGross == 0 && item.FeeScheme == p.FeeScheme {
			r.Status = "OBSERVED_MATCH"
			result.Matched++
		} else {
			result.Review++
		}
		result.Gross += gross
		result.Items = append(result.Items, r)
	}
	body, err := json.Marshal(result)
	if err != nil {
		return Result{}, err
	}
	result.Duplicate, err = writer.SaveOnce(ctx, p.ReportSHA256, body)
	return result, err
}

func verify(p Policy, raw []byte, now time.Time) (providers.Rendition, []extraItem, error) {
	var empty providers.Rendition
	fail := func() (providers.Rendition, []extraItem, error) { return empty, nil, providers.ErrVerification }
	if len(raw) == 0 || len(raw) > providers.MaxRenditionBytes {
		return fail()
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != p.ReportSHA256 {
		return fail()
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return fail()
	}
	b, readErr := io.ReadAll(io.LimitReader(gz, providers.MaxRenditionBytes+1))
	closeErr := gz.Close()
	if readErr != nil || closeErr != nil || len(b) > providers.MaxRenditionBytes {
		return fail()
	}
	parts := strings.Split(string(b), ".")
	if len(parts) != 3 {
		return fail()
	}
	h, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(h) > 64*1024 {
		return fail()
	}
	var header struct {
		Alg   string   `json:"alg"`
		Chain []string `json:"x5c"`
		Crit  []string `json:"crit"`
		B64   *bool    `json:"b64"`
	}
	if json.Unmarshal(h, &header) != nil || header.Alg != "RS512" || len(header.Chain) == 0 || len(header.Chain) > 8 || len(header.Crit) > 0 || (header.B64 != nil && !*header.B64) {
		return fail()
	}
	der, err := base64.StdEncoding.DecodeString(header.Chain[0])
	if err != nil {
		return fail()
	}
	certHash := sha256.Sum256(der)
	if hex.EncodeToString(certHash[:]) != p.CertificateSHA256 {
		return fail()
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return fail()
	}
	key, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || key.N.BitLen() < 2048 {
		return fail()
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fail()
	}
	digest := sha512.Sum512([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA512, digest[:], sig) != nil {
		return fail()
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fail()
	}
	var envelope struct {
		Issued int64           `json:"iat"`
		Report json.RawMessage `json:"report"`
	}
	if json.Unmarshal(payload, &envelope) != nil || envelope.Issued <= 0 || envelope.Issued > now.Add(5*time.Minute).Unix() {
		return fail()
	}
	var report providers.Rendition
	var extra struct {
		Items []extraItem `json:"items"`
	}
	if json.Unmarshal(envelope.Report, &report) != nil || json.Unmarshal(envelope.Report, &extra) != nil || report.Validate() != nil || len(report.Items) > 32 || report.RecipientUID != p.RecipientUID || report.Bank != p.Bank || report.Account != p.Account {
		return fail()
	}
	for idx, item := range report.Items {
		if !idPattern.MatchString(item.AttemptID) {
			return fail()
		}
		if _, err = providers.CLPInteger(extra.Items[idx].DelayedFee); err != nil {
			return fail()
		}
		date, e := providers.RenditionDate(extra.Items[idx].FundsDate)
		if e != nil || date.After(now.Add(5*time.Minute)) {
			return fail()
		}
		if item.IntegratorFee != "" && item.IntegratorFee != "0" {
			return fail()
		}
	}
	return report, extra.Items, nil
}
