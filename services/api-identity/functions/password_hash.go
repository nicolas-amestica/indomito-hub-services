package functions

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

const (
	argon2Memory  = 19 * 1024
	argon2Time    = 2
	argon2Threads = 1
	argon2KeyLen  = 32
	argon2SaltLen = 16
)

func protectPassword(password string) (string, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generar salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argon2Memory, argon2Time, argon2Threads, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func verifyPassword(encodedHash, password string) bool {
	if strings.HasPrefix(encodedHash, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(encodedHash), []byte(password)) == nil
	}
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil || memory < 7*1024 || memory > 64*1024 || iterations == 0 || iterations > 5 || threads == 0 || threads > 4 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 32 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != argon2KeyLen {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(want, got) == 1
}

func passwordHashNeedsUpgrade(encodedHash string) bool {
	if strings.HasPrefix(encodedHash, "$2") {
		return true
	}
	prefix := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$", argon2.Version, argon2Memory, argon2Time, argon2Threads)
	return !strings.HasPrefix(encodedHash, prefix)
}
