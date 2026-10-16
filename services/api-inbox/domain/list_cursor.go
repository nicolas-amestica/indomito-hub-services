package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrInvalidCursor indica formato, firma o binding inválidos.
	ErrInvalidCursor = errors.New("cursor invalido")
	// ErrExpiredCursor indica que un cursor auténtico superó su vigencia absoluta.
	ErrExpiredCursor = errors.New("cursor expirado")
)

// ListCursor contiene únicamente datos de control; los resúmenes congelados
// permanecen en LIST_RESULT y nunca viajan al cliente.
type ListCursor struct {
	Version           int    `json:"v"`
	SessionID         string `json:"sid"`
	Page              int    `json:"page"`
	QueryHash         string `json:"qh"`
	OwnerHash         string `json:"oh"`
	Slot              int    `json:"slot"`
	IssuedAt          int64  `json:"iat"`
	AbsoluteExpiresAt int64  `json:"exp"`
}

// ListCursorCodec firma y valida cursores opacos mediante HMAC-SHA256.
type ListCursorCodec struct {
	secret         []byte
	previousSecret []byte
	now            func() time.Time
}

// NewListCursorCodec construye un codec. El secreto debe provenir de SSM
// SecureString y tener al menos 32 bytes de entropía.
func NewListCursorCodec(secret []byte, now func() time.Time) (*ListCursorCodec, error) {
	return NewListCursorCodecWithPrevious(secret, nil, now)
}

// NewListCursorCodecWithPrevious permite validar cursores emitidos antes de una rotación.
func NewListCursorCodecWithPrevious(secret, previousSecret []byte, now func() time.Time) (*ListCursorCodec, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("secreto de cursor demasiado corto")
	}
	if len(previousSecret) > 0 && len(previousSecret) < 32 {
		return nil, fmt.Errorf("secreto anterior de cursor demasiado corto")
	}
	if now == nil {
		now = time.Now
	}
	return &ListCursorCodec{secret: append([]byte(nil), secret...), previousSecret: append([]byte(nil), previousSecret...), now: now}, nil
}

// Encode serializa y firma un cursor usando Base64URL sin padding.
func (c *ListCursorCodec) Encode(cursor ListCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("no se pudo serializar el cursor: %w", err)
	}
	signature := c.sign(payload)
	return encodeSignedCursor(payload, signature), nil
}

func encodeSignedCursor(payload, signature []byte) string {
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// Decode verifica firma, versión, binding y expiración del cursor.
func (c *ListCursorCodec) Decode(token, ownerID, queryHash string) (ListCursor, error) {
	payload, signature, err := splitCursor(token)
	if err != nil || (!hmac.Equal(signature, c.signWith(c.secret, payload)) && !c.matchesPrevious(signature, payload)) {
		return ListCursor{}, ErrInvalidCursor
	}
	var cursor ListCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return ListCursor{}, ErrInvalidCursor
	}
	if cursor.Version != 1 || cursor.SessionID == "" || cursor.Page < 2 || cursor.Slot < 0 || cursor.Slot > 2 ||
		cursor.QueryHash != queryHash || cursor.OwnerHash != HashCursorBinding(ownerID) ||
		cursor.IssuedAt <= 0 || cursor.AbsoluteExpiresAt <= cursor.IssuedAt {
		return ListCursor{}, ErrInvalidCursor
	}
	if c.now().Unix() >= cursor.AbsoluteExpiresAt {
		return ListCursor{}, ErrExpiredCursor
	}
	return cursor, nil
}

// HashCursorBinding evita incluir el identificador del usuario en el cursor.
func HashCursorBinding(value string) string {
	hash := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func (c *ListCursorCodec) sign(payload []byte) []byte {
	return c.signWith(c.secret, payload)
}

func (c *ListCursorCodec) matchesPrevious(signature, payload []byte) bool {
	return len(c.previousSecret) > 0 && hmac.Equal(signature, c.signWith(c.previousSecret, payload))
}

func (c *ListCursorCodec) signWith(secret, payload []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func splitCursor(token string) ([]byte, []byte, error) {
	separator := -1
	for index := range token {
		if token[index] == '.' {
			if separator >= 0 {
				return nil, nil, ErrInvalidCursor
			}
			separator = index
		}
	}
	if separator <= 0 || separator == len(token)-1 || len(token) > 2048 {
		return nil, nil, ErrInvalidCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(token[:separator])
	if err != nil {
		return nil, nil, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(token[separator+1:])
	if err != nil || len(signature) != sha256.Size {
		return nil, nil, ErrInvalidCursor
	}
	return payload, signature, nil
}
