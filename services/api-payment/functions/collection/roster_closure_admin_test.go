package collection

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRosterClosureAdminClosesOnlyRosterAndReplaysWithoutWriting(t *testing.T) {
	admin, db := adminFixture(t)
	view, err := admin.Contracts.LoadApproved(context.Background(), approvedID)
	if err != nil {
		t.Fatal(err)
	}
	setupBody, _ := json.Marshal(SetupRequest{ContractVersion: view.ContractVersion, FreeParticipantIDs: []string{view.Members[1].ID}})
	if response, setupErr := admin.HandleSetup(context.Background(), adminRequest("POST", string(setupBody))); setupErr != nil || response.StatusCode != 200 {
		t.Fatalf("setup failed: %+v %v", response, setupErr)
	}
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	app := RosterClosureApp{Accounts: admin.Accounts, Now: func() time.Time { current := now; now = now.Add(time.Minute); return current }}
	request := rosterMigrationRequest(RosterMigrationRequest{CommandID: paymentOperationID("closure-request"), Reason: "Nomina definitiva revisada"})
	response, err := app.Handle(context.Background(), request)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("closure handler failed: %+v %v", response, err)
	}
	plan, err := admin.Accounts.read(context.Background(), "TRIP#"+approvedID, "META")
	if err != nil || plan.Status != "ACTIVE" || !plan.RosterClosed || plan.RosterClosure == nil || plan.RosterClosure.Actor != "operator" {
		t.Fatalf("invalid closure state: %+v %v", plan, err)
	}
	commits := db.commits
	response, err = app.Handle(context.Background(), request)
	if err != nil || response.StatusCode != 200 || db.commits != commits {
		t.Fatalf("handler replay changed state: %+v %v commits=%d", response, err, db.commits)
	}
}

func TestRosterClosureAdminRejectsInjectedAuditAndPassengerSession(t *testing.T) {
	admin, db := adminFixture(t)
	app := RosterClosureApp{Accounts: admin.Accounts, Now: time.Now}
	request := rosterMigrationRequest(map[string]any{
		"commandId": paymentOperationID("closure-request"),
		"reason":    "Nomina definitiva revisada",
		"actor":     "forged",
	})
	response, err := app.Handle(context.Background(), request)
	if err != nil || response.StatusCode != 400 || db.commits != 0 {
		t.Fatalf("injected audit accepted: %+v %v", response, err)
	}
	request = rosterMigrationRequest(RosterMigrationRequest{CommandID: paymentOperationID("closure-request"), Reason: "Nomina definitiva revisada"})
	request.RequestContext.Authorizer.Lambda["paymentAccess"] = "passenger"
	response, err = app.Handle(context.Background(), request)
	if err != nil || response.StatusCode != 403 || db.commits != 0 {
		t.Fatal("passenger closure accepted")
	}
}
