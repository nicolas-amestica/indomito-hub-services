// Package renditioncapture conserva muestras no confiables solo para diagnóstico DEV.
// No importa lógica financiera ni dispone de permisos para modificar pagos.
package renditioncapture

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

const maxBody = 256 * 1024

// ErrOccupied indica un slot ya usado; las muestras nunca se sobreescriben.
var ErrOccupied = errors.New("capture slot occupied")

// Store limita el almacenamiento a slots inmutables y consulta solo su hash.
type Store interface {
	Digest(context.Context, string) (string, error)
	Put(context.Context, string, string, []byte) error
}

// App recibe evidencia sin autenticar: un 200 solo acredita almacenamiento.
type App struct {
	Store Store
	Until time.Time
	Now   func() time.Time
}

type evidence struct {
	Trust           string    `json:"trust"`
	ReceivedAt      time.Time `json:"receivedAt"`
	Digest          string    `json:"sha256"`
	ContentType     string    `json:"contentType"`
	ContentEncoding string    `json:"contentEncoding"`
	Body            []byte    `json:"bodyBase64"`
}

func response(status int) events.APIGatewayV2HTTPResponse {
	return events.APIGatewayV2HTTPResponse{StatusCode: status, Headers: map[string]string{"content-type": "application/json", "cache-control": "no-store"}, Body: `{"mode":"DEV_CAPTURE_ONLY","verified":false}`}
}

// Handle guarda como máximo 32 cuerpos de 256 KiB durante una ventana explícita.
// No descomprime, ejecuta ni confía en contenido recibido.
func (a App) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if req.RequestContext.HTTP.Method != "POST" {
		return response(405)
	}
	if a.Store == nil || a.Now == nil {
		return response(503)
	}
	now := a.Now().UTC()
	if !now.Before(a.Until) || a.Until.Sub(now) > 72*time.Hour {
		return response(503)
	}
	if len(req.Body) > base64.StdEncoding.EncodedLen(maxBody) {
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
	if len(raw) == 0 {
		return response(400)
	}
	if len(raw) > maxBody {
		return response(413)
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	header := func(name string) string {
		for key, value := range req.Headers {
			if strings.EqualFold(key, name) && len(value) <= 256 {
				return value
			}
		}
		return ""
	}
	body, err := json.Marshal(evidence{Trust: "UNVERIFIED_EXTERNAL_INPUT", ReceivedAt: now, Digest: digest, ContentType: header("content-type"), ContentEncoding: header("content-encoding"), Body: raw})
	if err != nil {
		return response(503)
	}
	for slot := range 32 {
		key := fmt.Sprintf("unverified/slot-%02d.json", slot)
		old, err := a.Store.Digest(ctx, key)
		if err != nil {
			return response(503)
		}
		if old == digest {
			return response(200)
		}
		if old != "" {
			continue
		}
		err = a.Store.Put(ctx, key, digest, body)
		if errors.Is(err, ErrOccupied) {
			// Otro aviso puede haber ocupado el slot después del HEAD.
			// Si era el mismo cuerpo, no consumir un segundo slot.
			current, readErr := a.Store.Digest(ctx, key)
			if readErr != nil {
				return response(503)
			}
			if current == digest {
				return response(200)
			}
			continue
		}
		if err != nil {
			return response(503)
		}
		return response(200)
	}
	return response(429)
}
