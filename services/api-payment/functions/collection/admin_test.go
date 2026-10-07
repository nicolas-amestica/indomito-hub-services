package collection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/paymentaccess"
)

const approvedID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

func adminFixture(t *testing.T) (AdminApp, *transactionDB) {
	t.Helper()
	item := map[string]any{"pk": "CONTRACT#" + approvedID, "sk": "METADATA", "id": approvedID, "version": 2, "status": "APPROVED", "content": map[string]any{
		"plan": map[string]any{"name": "Viaje de prueba"}, "trip": map[string]any{"departureDate": ""},
		"passengers": []any{map[string]any{"names": "Ana", "lastNames": "Prueba", "dni": "12.345.678-5"}, map[string]any{"names": "Pedro", "lastNames": "Prueba", "dni": "16.915.292-6"}},
		"payments":   map[string]any{"totalPassengers": 1, "freePassengers": 1, "pricePerPerson": 110000, "downPayment": 10000, "daysBeforePayment": 10, "installments": map[string]any{"quantity": 5, "startMonth": "01", "startYear": 2027, "startDay": 31}, "conditions": map[string]any{"refundPolicyVersion": 2}},
	}}
	raw, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatal(err)
	}
	db := &transactionDB{items: map[string]map[string]types.AttributeValue{itemKey(raw): raw}}
	return AdminApp{LookupSecret: strings.Repeat("test-only-", 4), Accounts: Service{DB: db, Table: "payments"}, Contracts: ContractSource{DB: db, Table: "contracts"}}, db
}
func adminRequest(method, body string) events.APIGatewayV2HTTPRequest {
	return events.APIGatewayV2HTTPRequest{Body: body, PathParameters: map[string]string{"id": approvedID}, RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: method}, Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]interface{}{"userId": "operator", "paymentAccess": "admin"}}}}
}

func TestSetupUsesApprovedContractAndDoesNotCountPledgedDeposit(t *testing.T) {
	app, db := adminFixture(t)
	ctx := context.Background()
	view, err := app.Contracts.LoadApproved(ctx, approvedID)
	if err != nil {
		t.Fatal(err)
	}
	if view.DueDates[1] != "2027-02-28" || view.DueDates[2] != "2027-03-31" {
		t.Fatal("contract calendar diverged")
	}
	body, err := json.Marshal(SetupRequest{ContractVersion: view.ContractVersion, FreeParticipantIDs: []string{view.Members[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := app.HandleSetup(ctx, adminRequest("POST", string(body)))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("setup failed: %+v %v", response, err)
	}
	account, err := app.Accounts.GetAccount(ctx, view.Members[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if account.DepositAgreed != 10000 || account.DepositReceived != 0 || account.Installments[0].Original != 20000 {
		t.Fatalf("wrong balance %+v", account)
	}
	lookupKey, keyErr := paymentaccess.RUTKey(app.LookupSecret, view.Members[0].DNI)
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	lookup, lookupErr := app.Accounts.read(ctx, "TRIP#"+approvedID, lookupKey)
	if lookupErr != nil || lookup.AccountID != account.ID || lookup.TripID != approvedID {
		t.Fatal("missing transactional RUT lookup")
	}
	free, err := app.Accounts.GetAccount(ctx, view.Members[1].ID)
	if err != nil || !free.Free || len(free.Installments) != 0 {
		t.Fatal("free passenger billed")
	}
	plan, err := app.Accounts.read(ctx, "TRIP#"+approvedID, "META")
	if err != nil || plan.Event == nil || plan.Event.Actor != "operator" {
		t.Fatal("missing setup audit")
	}
	commits := db.commits
	readResponse, readErr := app.HandleSetup(ctx, adminRequest("GET", ""))
	var envelope struct {
		Data SetupView `json:"data"`
	}
	if readErr != nil || json.Unmarshal([]byte(readResponse.Body), &envelope) != nil || envelope.Data.Status != "ACTIVE" || len(envelope.Data.FreeParticipantIDs) != 1 || envelope.Data.FreeParticipantIDs[0] != view.Members[1].ID {
		t.Fatalf("lost setup state after reload: %s", readResponse.Body)
	}
	response, err = app.HandleSetup(ctx, adminRequest("POST", string(body)))
	if err != nil || response.StatusCode != 200 || db.commits != commits {
		t.Fatal("setup duplicated on retry")
	}
}

func TestSetupRejectsForgedAmountsWrongVersionAndUnknownFreePassenger(t *testing.T) {
	for name, body := range map[string]string{
		"forged amount":    `{"contractVersion":2,"freeParticipantIds":[],"pricePerPayer":1}`,
		"wrong version":    `{"contractVersion":1,"freeParticipantIds":["unknown"]}`,
		"unknown free":     `{"contractVersion":2,"freeParticipantIds":["unknown"]}`,
		"wrong free count": `{"contractVersion":2,"freeParticipantIds":[]}`,
		"trailing JSON":    `{"contractVersion":2,"freeParticipantIds":[]} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			app, db := adminFixture(t)
			response, err := app.HandleSetup(context.Background(), adminRequest("POST", body))
			if err != nil || response.StatusCode < 400 || db.commits != 0 {
				t.Fatalf("unsafe request accepted: %+v %v", response, err)
			}
		})
	}
}

func TestSetupPublicIdentityCannotReadRoster(t *testing.T) {
	app, _ := adminFixture(t)
	req := adminRequest("GET", "")
	req.RequestContext.Authorizer.Lambda["paymentAccess"] = "passenger"
	response, err := app.HandleSetup(context.Background(), req)
	if err != nil || response.StatusCode != 403 || strings.Contains(response.Body, "Ana") {
		t.Fatal("public access exposed roster")
	}
	req.RequestContext.Authorizer = nil
	response, err = app.HandleSetup(context.Background(), req)
	if err != nil || response.StatusCode != 401 {
		t.Fatal("anonymous access accepted")
	}
}

func TestContractSourceRejectsDraftOrLegacyPolicy(t *testing.T) {
	for _, field := range []string{"status", "policy"} {
		t.Run(field, func(t *testing.T) {
			app, db := adminFixture(t)
			item := db.items["CONTRACT#"+approvedID+"/METADATA"]
			if field == "status" {
				item["status"] = &types.AttributeValueMemberS{Value: "DRAFT"}
			} else {
				content := item["content"].(*types.AttributeValueMemberM).Value
				payments := content["payments"].(*types.AttributeValueMemberM).Value
				conditions := payments["conditions"].(*types.AttributeValueMemberM).Value
				conditions["refundPolicyVersion"] = &types.AttributeValueMemberN{Value: "0"}
			}
			if _, err := app.Contracts.LoadApproved(context.Background(), approvedID); err == nil {
				t.Fatal("unapproved or historical contract silently activated")
			}
		})
	}
}
