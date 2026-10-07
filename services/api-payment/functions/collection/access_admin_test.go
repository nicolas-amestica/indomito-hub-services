package collection

import (
	"context"
	"strings"
	"testing"
	"time"

	"ind-hub-api-gox-sls-pri-gh/libs/paymentaccess"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestRotateAndRevokeTripCodeInvalidatesOldAndCurrentSessions(t *testing.T) {
	s, db := serviceFixture(t)
	tripID := approvedID
	secret := strings.Repeat("access-secret-", 3)
	oldKey, err := paymentaccess.CodeKey(secret, "AB23CD")
	if err != nil {
		t.Fatal(err)
	}
	saveLookupRow(t, db, record{PK: "TRIP#" + tripID, SK: "APPROVAL", TripID: tripID, CodeKey: oldKey})
	saveLookupRow(t, db, record{PK: oldKey, SK: "META", TripID: tripID, CodeVersion: 1, Status: "RESERVED_APPROVED"})
	app := AdminApp{LookupSecret: secret, Accounts: s}
	audit := domainAuditForAccess("rotate")
	rotated, err := app.changeTripAccess(context.Background(), tripID, "ROTATE", 1, audit)
	if err != nil || rotated.Version != 2 || len(rotated.TripCode) != 6 || rotated.Status != "ACTIVE" {
		t.Fatalf("rotate=%+v %v", rotated, err)
	}
	old, _ := s.read(context.Background(), oldKey, "META")
	newKey, _ := paymentaccess.CodeKey(secret, rotated.TripCode)
	current, _ := s.read(context.Background(), newKey, "META")
	if old.Status != "REVOKED" || current.Status != "RESERVED_APPROVED" {
		t.Fatal("code switch was not atomic")
	}
	commits := db.commits
	again, err := app.changeTripAccess(context.Background(), tripID, "ROTATE", 1, audit)
	if err != nil || again.TripCode != rotated.TripCode || db.commits != commits {
		t.Fatal("rotation replay generated another code")
	}
	revoked, err := app.changeTripAccess(context.Background(), tripID, "REVOKE", 2, domainAuditForAccess("revoke"))
	if err != nil || revoked.Status != "REVOKED" || revoked.TripCode != "" {
		t.Fatalf("revoke=%+v %v", revoked, err)
	}
	current, _ = s.read(context.Background(), newKey, "META")
	if current.Status != "REVOKED" {
		t.Fatal("current code remained usable")
	}
}

func domainAuditForAccess(id string) domain.Audit {
	return domain.Audit{CommandID: paymentOperationID("access-test:" + id), Actor: "operator", Reason: "Cambio de acceso autorizado", RecordedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
}
