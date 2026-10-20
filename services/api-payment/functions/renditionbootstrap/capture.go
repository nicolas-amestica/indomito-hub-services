// Package renditionbootstrap captura evidencia autenticada sin acceso contable.
package renditionbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

// Store conserva un único documento inmutable por ventana; admite su duplicado.
type Store interface {
	Save(context.Context, string, string, []byte) error
}

// App no tiene dependencias financieras ni acceso a la base de datos.
type App struct {
	Store    Store
	Verifier providers.RenditionCaptureVerifier
	Enabled  bool
	Until    time.Time
	Now      func() time.Time
}

type evidence struct {
	Trust      string              `json:"trust"`
	ReceivedAt time.Time           `json:"receivedAt"`
	Digest     string              `json:"sha256"`
	Body       []byte              `json:"bodyBase64"`
	Report     providers.Rendition `json:"report"`
}

func response(status int) events.APIGatewayV2HTTPResponse {
	return events.APIGatewayV2HTTPResponse{
		StatusCode: status,
		Headers:    map[string]string{"content-type": "application/json", "cache-control": "no-store"},
		Body:       `{"mode":"CAPTURE_ONLY","destinationVerified":false,"reconciled":false}`,
	}
}

// Handle devuelve 200 solo si conserva evidencia autenticada de forma durable.
func (a App) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if req.RequestContext.HTTP.Method != "POST" {
		return response(405)
	}
	if !a.Enabled || a.Store == nil || a.Now == nil {
		return response(503)
	}
	now := a.Now().UTC()
	if now.IsZero() || !now.Before(a.Until) || a.Until.Sub(now) > 72*time.Hour {
		return response(503)
	}
	if len(req.Body) > base64.StdEncoding.EncodedLen(providers.MaxRenditionBytes) {
		return response(413)
	}
	raw := []byte(req.Body)
	if req.IsBase64Encoded {
		var err error
		raw, err = base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return response(400)
		}
	}
	if len(raw) > providers.MaxRenditionBytes {
		return response(413)
	}
	report, err := a.Verifier.Verify(raw, now)
	if err != nil {
		return response(400)
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	body, err := json.Marshal(evidence{
		Trust: "AUTHENTICATED_DESTINATION_UNVERIFIED", ReceivedAt: now,
		Digest: digest, Body: raw, Report: report,
	})
	if err != nil {
		return response(503)
	}
	key := fmt.Sprintf("rendition-bootstrap/prd/%d/report.json", a.Until.Unix())
	if err = a.Store.Save(ctx, key, digest, body); err != nil {
		return response(503)
	}
	return response(200)
}
