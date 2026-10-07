package collection

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"time"

	"github.com/oklog/ulid/v2"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// PassengerSession contiene una credencial breve; nunca debe registrarse en logs ni URL.
type PassengerSession struct {
	AccessToken string `json:"accessToken"`
	ExpiresAt   int64  `json:"expiresAt"`
}

var passengerCodeKey = regexp.MustCompile(`^CODE#[a-f0-9]{64}$`)

// issuePassengerSession firma solamente identidades resueltas por el servidor.
// La validación del JWT pertenece exclusivamente al authorizer compartido.
func issuePassengerSession(secret, accountID, tripID, codeKey string, now time.Time) (PassengerSession, error) {
	if len(secret) < 32 || now.IsZero() || now.Unix() <= 0 || !passengerCodeKey.MatchString(codeKey) {
		return PassengerSession{}, domain.ErrInvalid
	}
	for _, id := range []string{accountID, tripID} {
		parsed, err := ulid.ParseStrict(id)
		if err != nil || parsed.String() != id {
			return PassengerSession{}, domain.ErrInvalid
		}
	}
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return PassengerSession{}, err
	}
	expires := now.Unix() + 600
	claims := struct {
		Issuer    string `json:"iss"`
		Audience  string `json:"aud"`
		Use       string `json:"tokenUse"`
		Subject   string `json:"sub"`
		TripID    string `json:"tripId"`
		SessionID string `json:"jti"`
		CodeKey   string `json:"codeKey"`
		Issued    int64  `json:"iat"`
		Expires   int64  `json:"exp"`
	}{"indomito-payments-dev", "indomito-payment-portal-dev", "passenger-payment", accountID, tripID, id.String(), codeKey, now.Unix(), expires}
	payload, err := json.Marshal(claims)
	if err != nil {
		return PassengerSession{}, err
	}
	unsigned := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err = mac.Write([]byte(unsigned)); err != nil {
		return PassengerSession{}, err
	}
	return PassengerSession{AccessToken: unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), ExpiresAt: expires}, nil
}
