package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// KhipuError identifica el fallo sin conservar cuerpos, URLs ni credenciales.
// Retryable describe consultas de lectura; nunca autoriza repetir una creación.
type KhipuError struct {
	Code       string
	HTTPStatus int
	Retryable  bool
	RetryAt    time.Time
}

// Error entrega únicamente datos operativos redactados.
func (e *KhipuError) Error() string {
	return fmt.Sprintf("khipu %s (HTTP %d)", e.Code, e.HTTPStatus)
}

// Unwrap conserva compatibilidad con errores de proveedor existentes.
func (e *KhipuError) Unwrap() error { return ErrProvider }

func khipuHTTPError(status int, retryAfter string, now time.Time) *KhipuError {
	e := &KhipuError{HTTPStatus: status, Code: "UNEXPECTED_HTTP_STATUS"}
	switch {
	case status == http.StatusBadRequest:
		e.Code = "INVALID_REQUEST"
	case status == http.StatusUnauthorized:
		e.Code = "AUTHENTICATION_FAILED"
	case status == http.StatusForbidden:
		e.Code = "PERMISSION_DENIED"
	case status == http.StatusNotFound:
		e.Code = "PAYMENT_NOT_FOUND"
	case status == http.StatusRequestTimeout:
		e.Code, e.Retryable = "REQUEST_TIMEOUT", true
	case status == http.StatusTooManyRequests:
		e.Code, e.Retryable = "RATE_LIMITED", true
	case status >= 500 && status < 600:
		e.Code, e.Retryable = "SERVICE_UNAVAILABLE", true
	}
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		e.RetryAt = khipuRetryAt(retryAfter, now)
	}
	return e
}

func khipuTransportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return &KhipuError{Code: "REQUEST_TIMEOUT", Retryable: true}
	}
	return &KhipuError{Code: "NETWORK_ERROR", Retryable: true}
}

// khipuRetryAt admite segundos enteros o HTTP-date (RFC 9110, sección 10.2.3).
func khipuRetryAt(raw string, now time.Time) time.Time {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 128 {
		return time.Time{}
	}
	if allDigits(raw) {
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && seconds <= int64((1<<63-1)/time.Second) {
			return now.Add(time.Duration(seconds) * time.Second)
		}
		return time.Time{}
	}
	if at, err := http.ParseTime(raw); err == nil && at.After(now) {
		return at
	}
	return time.Time{}
}

func allDigits(raw string) bool {
	for _, c := range raw {
		if c < '0' || c > '9' {
			return false
		}
	}
	return raw != ""
}
