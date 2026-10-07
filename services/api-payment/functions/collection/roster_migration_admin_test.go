package collection

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

func rosterMigrationRequest(body any) events.APIGatewayV2HTTPRequest {
	raw, _ := json.Marshal(body)
	request := adminRequest("POST", string(raw))
	request.PathParameters = map[string]string{"tripId": approvedID}
	return request
}

func TestRosterMigrationAdminDerivesIdentityFromApprovedContract(t *testing.T) {
	admin, db := adminFixture(t)
	view, err := admin.Contracts.LoadApproved(context.Background(), approvedID)
	if err != nil {
		t.Fatal(err)
	}
	setupBody, _ := json.Marshal(SetupRequest{ContractVersion: view.ContractVersion, FreeParticipantIDs: []string{view.Members[1].ID}})
	if response, setupErr := admin.HandleSetup(context.Background(), adminRequest("POST", string(setupBody))); setupErr != nil || response.StatusCode != 200 {
		t.Fatalf("setup failed: %+v %v", response, setupErr)
	}
	db.mu.Lock()
	for _, member := range view.Members {
		delete(db.items, "TRIP#"+approvedID+"/MEMBER#"+member.ID)
	}
	delete(db.items["TRIP#"+approvedID+"/META"], "rosterSchemaVersion")
	db.mu.Unlock()
	db.commits = 0
	now := time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC)
	app := RosterMigrationApp{Accounts: admin.Accounts, Contracts: admin.Contracts, Now: func() time.Time { current := now; now = now.Add(time.Minute); return current }}
	body := RosterMigrationRequest{CommandID: paymentOperationID("migration-request"), Reason: "Habilitar nomina administrativa"}
	request := rosterMigrationRequest(body)
	response, err := app.Handle(context.Background(), request)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("migration handler failed: %+v %v", response, err)
	}
	page, err := admin.Accounts.ListRoster(context.Background(), approvedID, "")
	if err != nil || len(page.Items) != 2 || page.Items[0].Name == "" || page.Items[0].Document == "" {
		t.Fatalf("approved identities not projected: %+v %v", page, err)
	}
	commits := db.commits
	response, err = app.Handle(context.Background(), request)
	if err != nil || response.StatusCode != 200 || db.commits != commits {
		t.Fatalf("handler replay changed state: %+v %v commits=%d", response, err, db.commits)
	}
}

func TestRosterMigrationAdminRejectsInjectedRosterAndPassengerSession(t *testing.T) {
	admin, db := adminFixture(t)
	app := RosterMigrationApp{Accounts: admin.Accounts, Contracts: admin.Contracts, Now: time.Now}
	request := rosterMigrationRequest(map[string]any{"commandId": paymentOperationID("migration-request"), "reason": "Motivo valido", "profiles": []any{map[string]any{"name": "Falsificado"}}})
	response, err := app.Handle(context.Background(), request)
	if err != nil || response.StatusCode != 400 || db.commits != 0 {
		t.Fatalf("injected roster accepted: %+v %v", response, err)
	}
	request.RequestContext.Authorizer.Lambda["paymentAccess"] = "passenger"
	response, err = app.Handle(context.Background(), request)
	if err != nil || response.StatusCode != 403 || db.commits != 0 {
		t.Fatal("passenger migration accepted")
	}
}
