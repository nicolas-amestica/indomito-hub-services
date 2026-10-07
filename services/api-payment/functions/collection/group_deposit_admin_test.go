package collection

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

func groupDepositAdminRequest(method, tripID, commandID string, body any) events.APIGatewayV2HTTPRequest {
	raw, _ := json.Marshal(body)
	return events.APIGatewayV2HTTPRequest{Body: string(raw), PathParameters: map[string]string{"tripId": tripID, "commandId": commandID}, RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: method}, Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]any{"userId": "operator", "paymentAccess": "admin"}}}}
}

func TestGroupDepositAdminRegistersAndRecoversTheSameCommand(t *testing.T) {
	service, db, input, _ := groupDepositFixture(t)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	app := GroupDepositApp{Accounts: service, Now: func() time.Time { current := now; now = now.Add(time.Minute); return current }}
	clientCommand := paymentOperationID("group-client-command")
	body := GroupDepositRequest{CommandID: clientCommand, Amount: input.Amount, Reference: "group-001", EffectiveDate: input.EffectiveDate, Email: input.ReceiptEmail, Reason: "Abono grupal confirmado"}
	request := groupDepositAdminRequest("POST", input.TripID, "", body)
	response, err := app.Handle(context.Background(), request)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("register: %+v %v", response, err)
	}
	commits := db.commits
	response, err = app.Handle(context.Background(), request)
	if err != nil || response.StatusCode != 200 || db.commits != commits {
		t.Fatalf("replay: %+v %v commits=%d", response, err, db.commits)
	}
	get := groupDepositAdminRequest("GET", input.TripID, clientCommand, nil)
	response, err = app.Handle(context.Background(), get)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("get: %+v %v", response, err)
	}
}

func TestGroupDepositAdminRejectsInjectedAuditAndPassenger(t *testing.T) {
	service, db, input, _ := groupDepositFixture(t)
	app := GroupDepositApp{Accounts: service, Now: time.Now}
	request := groupDepositAdminRequest("POST", input.TripID, "", map[string]any{"commandId": paymentOperationID("group-client-command"), "amount": 5, "reference": "group-001", "effectiveDate": "2026-10-05", "email": "group@example.test", "reason": "Abono grupal confirmado", "audit": map[string]any{"actor": "forged"}})
	response, err := app.Handle(context.Background(), request)
	if err != nil || response.StatusCode != 400 || db.commits != 0 {
		t.Fatalf("injected audit accepted: %+v %v", response, err)
	}
	request = groupDepositAdminRequest("POST", input.TripID, "", GroupDepositRequest{CommandID: paymentOperationID("group-client-command"), Amount: 5, Reference: "group-001", EffectiveDate: "2026-10-05", Email: "group@example.test", Reason: "Abono grupal confirmado"})
	request.RequestContext.Authorizer.Lambda["paymentAccess"] = "passenger"
	response, err = app.Handle(context.Background(), request)
	if err != nil || response.StatusCode != 403 || db.commits != 0 {
		t.Fatal("passenger group deposit accepted")
	}
}
