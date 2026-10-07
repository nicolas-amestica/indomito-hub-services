package collection

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/paymentaccess"
)

func annexAdminRequest(method, tripID, annexID string, body any) events.APIGatewayV2HTTPRequest {
	raw, _ := json.Marshal(body)
	return events.APIGatewayV2HTTPRequest{Body: string(raw), PathParameters: map[string]string{"tripId": tripID, "annexId": annexID}, RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: method}, Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]any{"userId": "operator", "paymentAccess": "admin"}}}}
}

func TestAnnexAdminCreatesPreviewsAndAppliesWithoutPersistingRawRUT(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	now := time.Date(2026, 10, 4, 16, 0, 0, 0, time.UTC)
	secret := "annex-admin-test-secret-with-32-bytes"
	app := AnnexAdminApp{Accounts: s, LookupSecret: secret, Now: func() time.Time { current := now; now = now.Add(time.Minute); return current }}
	admission := input.Admissions[0]
	body := AnnexDraftRequest{ID: input.ID, Reason: "Cambio de nomina confirmado", Withdrawals: input.Withdrawals, Admissions: []AnnexAdmissionRequest{{AccountID: admission.AccountID, ParticipantID: admission.ParticipantID, Name: "Pasajero entrante", DNI: "12.345.678-5", DepositAgreed: admission.DepositAgreed, Installments: admission.Installments}}}
	response, err := app.HandleCreateAnnex(context.Background(), annexAdminRequest("POST", input.TripID, "", body))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("create failed: %+v %v", response, err)
	}
	createCommits := db.commits
	response, err = app.HandleCreateAnnex(context.Background(), annexAdminRequest("POST", input.TripID, "", body))
	if err != nil || response.StatusCode != 200 || db.commits != createCommits {
		t.Fatalf("draft retry changed state: %+v %v commits=%d", response, err, db.commits)
	}
	lookup, _ := paymentaccess.RUTKey(secret, "12.345.678-5")
	proposal, err := s.read(context.Background(), "TRIP#"+input.TripID, "ANNEX#"+input.ID+"#PROPOSAL#"+admission.AccountID)
	if err != nil || proposal.LookupKey != lookup {
		t.Fatalf("hashed lookup missing: %+v %v", proposal, err)
	}
	if _, err = s.read(context.Background(), "TRIP#"+input.TripID, lookup); err == nil {
		t.Fatal("draft published public identity before approval")
	}
	preview, err := app.HandlePreviewAnnex(context.Background(), annexAdminRequest("GET", input.TripID, input.ID, nil))
	if err != nil || preview.StatusCode != 200 {
		t.Fatalf("preview failed: %+v %v", preview, err)
	}
	approval := AnnexApprovalRequest{CommandID: paymentOperationID("approval-request"), Reason: "Impacto y pasajeros revisados"}
	applyReq := annexAdminRequest("POST", input.TripID, input.ID, approval)
	response, err = app.HandleApplyAnnex(context.Background(), applyReq)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("apply failed: %+v %v", response, err)
	}
	commits := db.commits
	response, err = app.HandleApplyAnnex(context.Background(), applyReq)
	if err != nil || response.StatusCode != 200 || db.commits != commits {
		t.Fatalf("lost-response retry changed state: %+v %v commits=%d", response, err, db.commits)
	}
	root, _ := s.read(context.Background(), "TRIP#"+input.TripID, "ANNEX#"+input.ID)
	if root.Status != "APPLIED" || root.Approval == nil || root.Approval.Actor != "operator" || root.Event == nil || root.Event.Actor != input.Audit.Actor {
		t.Fatalf("separate audits missing: %+v", root)
	}
	roster, rosterErr := s.ListRoster(context.Background(), input.TripID, "")
	byID := map[string]RosterProjection{}
	for _, member := range roster.Items {
		byID[member.AccountID] = member
	}
	if rosterErr != nil || len(roster.Items) != 2 || byID[input.Withdrawals[0].AccountID].Active || byID[admission.AccountID].Name != "Pasajero entrante" || !byID[admission.AccountID].Active {
		t.Fatalf("roster projection not updated: %+v %v", roster, rosterErr)
	}
}

func TestAnnexAdminRejectsPublicIdentityAndInjectedAudit(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	lookupSigningKeyFixture := "annex-admin-test-key-with-32-bytes"
	app := AnnexAdminApp{Accounts: s, LookupSecret: lookupSigningKeyFixture, Now: time.Now}
	req := annexAdminRequest("POST", input.TripID, "", map[string]any{"id": input.ID, "reason": "Motivo valido", "withdrawals": input.Withdrawals, "admissions": input.Admissions, "audit": map[string]any{"actor": "forged"}})
	response, err := app.HandleCreateAnnex(context.Background(), req)
	if err != nil || response.StatusCode != 400 || db.commits != 0 {
		t.Fatalf("injected audit accepted: %+v %v", response, err)
	}
	req.RequestContext.Authorizer.Lambda["paymentAccess"] = "passenger"
	response, err = app.HandleCreateAnnex(context.Background(), req)
	if err != nil || response.StatusCode != 403 || db.commits != 0 {
		t.Fatal("passenger identity accepted")
	}
}
