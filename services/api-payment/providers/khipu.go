// Package providers contains server-side payment integrations, never browser credentials.
package providers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrProvider     = errors.New("payment provider request failed; reconcile before retrying creation")
	ErrVerification = errors.New("payment verification failed")
	khipuID         = regexp.MustCompile(`^[a-zA-Z0-9]{12}$`)
	clpAmount       = regexp.MustCompile(`^[0-9]{1,13}(\.0{1,4})?$`)
)

// Khipu is deliberately not wired into the local simulator. Creating an instance
// does not perform requests. Credentials must come from a server secret store.
type Khipu struct {
	apiKey     string
	receiverID int64
	client     *http.Client
}

func NewKhipu(apiKey string, receiverID int64) (*Khipu, error) {
	if strings.TrimSpace(apiKey) == "" || receiverID <= 0 {
		return nil, ErrVerification
	}
	return &Khipu{apiKey: apiKey, receiverID: receiverID, client: &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

type CheckoutRequest struct {
	BankID        string
	TransactionID string
	Subject       string
	Amount        int64
	ReturnURL     string
	CancelURL     string
	NotifyURL     string
	ExpiresAt     time.Time
}

type Checkout struct {
	PaymentID  string `json:"payment_id"`
	PaymentURL string `json:"payment_url"`
}

// Create never retries POST: transaction_id is a correlation reference, not an
// assumed upstream idempotency guarantee. An ambiguous result needs reconciliation.
func (k *Khipu) Create(ctx context.Context, in CheckoutRequest) (Checkout, error) {
	var out Checkout
	if in.Amount <= 0 || in.Amount > 1_000_000_000_000 || len(in.TransactionID) == 0 || len(in.TransactionID) > 100 || len(in.Subject) == 0 || len(in.Subject) > 255 || !in.ExpiresAt.After(time.Now()) {
		return out, ErrVerification
	}
	for _, raw := range []string{in.ReturnURL, in.CancelURL, in.NotifyURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return out, ErrVerification
		}
	}
	body, err := json.Marshal(map[string]any{
		"bank_id": in.BankID,
		"amount":  in.Amount, "currency": "CLP", "subject": in.Subject,
		"transaction_id": in.TransactionID, "return_url": in.ReturnURL,
		"cancel_url": in.CancelURL, "notify_url": in.NotifyURL,
		"notify_api_version": "3.0", "expires_date": in.ExpiresAt.UTC().Format(time.RFC3339),
		"send_email": false, "send_reminders": false,
	})
	if err != nil {
		return out, ErrVerification
	}
	if err := k.request(ctx, http.MethodPost, "/v3/payments", body, &out); err != nil {
		return Checkout{}, err
	}
	u, err := url.Parse(out.PaymentURL)
	if err != nil || !khipuID.MatchString(out.PaymentID) || u.Scheme != "https" || u.User != nil || u.Port() != "" || (u.Hostname() != "khipu.com" && u.Hostname() != "app.khipu.com") {
		return Checkout{}, ErrVerification
	}
	return out, nil
}

// DevelopmentBank bloquea creación DEV si aparece un banco no identificado como DemoBank.
// Es una defensa adicional: las credenciales deben ser de una cuenta desarrollador.
func (k *Khipu) DevelopmentBank(ctx context.Context) (string, error) {
	var out struct {
		Banks []struct {
			ID   string `json:"bank_id"`
			Name string `json:"name"`
		} `json:"banks"`
	}
	if err := k.request(ctx, http.MethodGet, "/v3/banks", nil, &out); err != nil {
		return "", err
	}
	if len(out.Banks) == 0 {
		return "", ErrVerification
	}
	for _, bank := range out.Banks {
		name := strings.ReplaceAll(strings.ToLower(bank.Name), " ", "")
		if bank.ID == "" || !strings.Contains(name, "demobank") {
			return "", ErrVerification
		}
	}
	return out.Banks[0].ID, nil
}

// VerifiedPayment intentionally excludes bank account, RUT, payer name and email.
type VerifiedPayment struct {
	PaymentID        string    `json:"payment_id"`
	TransactionID    string    `json:"transaction_id"`
	ReceiverID       int64     `json:"receiver_id"`
	Amount           string    `json:"amount"`
	Currency         string    `json:"currency"`
	Status           string    `json:"status"`
	StatusDetail     string    `json:"status_detail"`
	ConciliationDate time.Time `json:"conciliation_date"`
}

// Verify checks the authoritative API, not the browser return or webhook values.
// Refunded/reversed/manually marked and pending payments require separate handling.
func (k *Khipu) Verify(ctx context.Context, paymentID, transactionID string, amount int64) (VerifiedPayment, error) {
	var out VerifiedPayment
	if !khipuID.MatchString(paymentID) || transactionID == "" || amount <= 0 || amount > 1_000_000_000_000 {
		return out, ErrVerification
	}
	if err := k.request(ctx, http.MethodGet, "/v3/payments/"+paymentID, nil, &out); err != nil {
		return VerifiedPayment{}, err
	}
	if !clpAmount.MatchString(out.Amount) {
		return VerifiedPayment{}, ErrVerification
	}
	whole, _, _ := strings.Cut(out.Amount, ".")
	actual, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || actual != amount || out.PaymentID != paymentID || out.TransactionID != transactionID || out.ReceiverID != k.receiverID || out.Currency != "CLP" || out.Status != "done" || out.StatusDetail != "normal" || out.ConciliationDate.IsZero() {
		return VerifiedPayment{}, ErrVerification
	}
	return out, nil
}

func (k *Khipu) request(ctx context.Context, method, path string, body []byte, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, "https://payment-api.khipu.com"+path, bytes.NewReader(body))
	if err != nil {
		return ErrProvider
	}
	req.Header.Set("x-api-key", k.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := k.client.Do(req)
	if err != nil {
		return ErrProvider
	}
	// El error de cierre no cambia el resultado del cuerpo ya leído y validado.
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ErrProvider
	}
	const maxResponse = 64 * 1024
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse || json.Unmarshal(data, result) != nil {
		return ErrProvider
	}
	return nil
}

// VerifyKhipuSignature authenticates the exact raw webhook body, before parsing.
// Replay protection additionally requires durable payment/event deduplication.
func VerifyKhipuSignature(raw []byte, header, secret string, now time.Time) error {
	if len(raw) == 0 || len(raw) > 64*1024 || len(header) > 256 || secret == "" {
		return ErrVerification
	}
	parts := strings.Split(header, ",")
	if len(parts) != 2 {
		return ErrVerification
	}
	values := map[string]string{}
	for _, part := range parts {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || (key != "t" && key != "s") || values[key] != "" || value == "" {
			return ErrVerification
		}
		values[key] = value
	}
	stamp, err := strconv.ParseInt(values["t"], 10, 64)
	if err != nil || stamp < now.Add(-5*time.Minute).UnixMilli() || stamp > now.Add(5*time.Minute).UnixMilli() {
		return ErrVerification
	}
	signature, err := base64.StdEncoding.DecodeString(values["s"])
	if err != nil || len(signature) != sha256.Size {
		return ErrVerification
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(values["t"] + "."))
	mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return ErrVerification
	}
	return nil
}
