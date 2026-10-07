package collection

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestGroupDiscountAdminRequiresSeparateApprovalAndRecovers(t *testing.T) {
	service, _, input, _ := groupDiscountFixture(t)
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	app := GroupDiscountApp{Accounts: service, Now: func() time.Time { return now }}
	publicID := paymentOperationID("public-discount")
	body := GroupDiscountRequest{ID: publicID, BasisPoints: 1000, Reason: "Ayuda extraordinaria documentada"}
	response, err := app.Handle(context.Background(), annexAdminRequest("POST", input.TripID, "", body))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("create: %+v %v", response, err)
	}
	var envelope struct {
		Data GroupDiscountState `json:"data"`
	}
	if json.Unmarshal([]byte(response.Body), &envelope) != nil || envelope.Data.Status != "DRAFT" || envelope.Data.Amount != 270 {
		t.Fatalf("draft response: %s", response.Body)
	}
	get := annexAdminRequest("GET", input.TripID, "", nil)
	get.PathParameters["discountId"] = publicID
	response, err = app.Handle(context.Background(), get)
	if err != nil || response.StatusCode != 200 {
		t.Fatal("durable get failed")
	}
	approve := annexAdminRequest("POST", input.TripID, "", GroupDiscountApprovalRequest{CommandID: paymentOperationID("approval-public"), Reason: "Impacto y beneficiarios revisados"})
	approve.PathParameters["discountId"] = publicID
	response, err = app.Handle(context.Background(), approve)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("approve: %+v %v", response, err)
	}
	response, err = app.Handle(context.Background(), approve)
	if err != nil || response.StatusCode != 200 {
		t.Fatal("approval replay failed")
	}
}

func TestGroupDiscountAdminRejectsPassengerAndInjectedAudit(t *testing.T) {
	service, db, input, _ := groupDiscountFixture(t)
	app := GroupDiscountApp{Accounts: service, Now: time.Now}
	req := annexAdminRequest("POST", input.TripID, "", map[string]any{"id": paymentOperationID("public"), "basisPoints": 1000, "reason": "Motivo valido", "audit": map[string]any{"actor": "forged"}})
	response, err := app.Handle(context.Background(), req)
	if err != nil || response.StatusCode != 400 || db.commits != 0 {
		t.Fatal("injected audit accepted")
	}
	req = annexAdminRequest("POST", input.TripID, "", GroupDiscountRequest{ID: paymentOperationID("public"), BasisPoints: 1000, Reason: "Motivo valido"})
	req.RequestContext.Authorizer.Lambda["paymentAccess"] = "passenger"
	response, err = app.Handle(context.Background(), req)
	if err != nil || response.StatusCode != 403 || db.commits != 0 {
		t.Fatal("passenger accepted")
	}
}
