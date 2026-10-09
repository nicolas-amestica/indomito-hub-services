package functions

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestProtectPasswordUsesArgon2idAndVerifies(t *testing.T) {
	hash, err := protectPassword("una-clave-segura-2026")
	if err != nil {
		t.Fatalf("protectPassword devolvió error: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash no usa Argon2id: %s", hash)
	}
	if !verifyPassword(hash, "una-clave-segura-2026") {
		t.Fatal("la clave correcta fue rechazada")
	}
	if verifyPassword(hash, "clave-equivocada") {
		t.Fatal("una clave incorrecta fue aceptada")
	}
	if passwordHashNeedsUpgrade(hash) {
		t.Fatal("un hash Argon2id actual no debe requerir migración")
	}
}

func TestVerifyPasswordSupportsLegacyBcryptForGradualMigration(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("clave-legacy-segura"), 10)
	if err != nil {
		t.Fatalf("no fue posible preparar bcrypt: %v", err)
	}
	if !verifyPassword(string(hash), "clave-legacy-segura") {
		t.Fatal("el hash bcrypt heredado fue rechazado")
	}
	if !passwordHashNeedsUpgrade(string(hash)) {
		t.Fatal("bcrypt debe marcarse para migración")
	}
}

func TestPasswordResetTokenIsOpaqueAndBoundToUser(t *testing.T) {
	token, hash, err := newPasswordResetToken("user-01")
	if err != nil {
		t.Fatalf("newPasswordResetToken devolvió error: %v", err)
	}
	userID, ok := passwordResetUserID(token)
	if !ok || userID != "user-01" {
		t.Fatalf("usuario inesperado: %q, ok=%v", userID, ok)
	}
	if token == hash || len(hash) != 64 {
		t.Fatal("el token no se almacenaría como SHA-256")
	}
}
