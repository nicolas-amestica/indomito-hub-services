// Package paymentaccess define claves opacas compartidas por contratos y cobranza.
package paymentaccess

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
)

// Digest separa contextos criptográficos para no reutilizar un índice entre propósitos.
func Digest(secret, purpose, value string) (string, error) {
	if len(secret) < 32 || purpose == "" {
		return "", errors.New("configuración de índice privado inválida")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, err := mac.Write([]byte(purpose + ":" + value))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// CodeKey obtiene el índice de un código normalizado de seis caracteres, no una identidad.
func CodeKey(secret, code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 6 {
		return "", errors.New("código inválido")
	}
	for _, c := range code {
		if !strings.ContainsRune("23456789ABCDEFGHJKLMNPQRSTUVWXYZ", c) {
			return "", errors.New("código inválido")
		}
	}
	digest, err := Digest(secret, "trip-code:v1", code)
	return "CODE#" + digest, err
}

// RUTKey exige dígito verificador; nunca acepta un DNI numérico como fallback.
func RUTKey(secret, rut string) (string, error) {
	if len(rut) > 20 || !lambdautil.IsValidRUT(rut) {
		return "", errors.New("RUT inválido")
	}
	normalized := strings.ToUpper(strings.NewReplacer(".", "", "-", "", " ", "").Replace(strings.TrimSpace(rut)))
	digest, err := Digest(secret, "passenger-rut:v1", normalized)
	return "RUT#" + digest, err
}
