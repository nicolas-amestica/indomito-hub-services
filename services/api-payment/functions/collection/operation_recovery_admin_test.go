package collection

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRecoverSupplierOperationAfterResponseIsLost(t *testing.T) {
	s, _, tripID, supplierID := supplierFixture(t)
	clientID := paymentOperationID("client-supplier-operation")
	request := adminRequest("POST", `{"commandId":"`+clientID+`","version":0,"operation":"CREATE","name":"Hotel Andes","service":"Alojamiento","committed":100000,"reason":"Contrato de servicio revisado"}`)
	request.PathParameters = map[string]string{"tripId": tripID, "supplierId": supplierID}
	response, err := (TreasuryAdminApp{Accounts: s, Now: func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }}).HandleSuppliers(context.Background(), request)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("create=%+v %v", response, err)
	}
	recovery := adminRequest("GET", "")
	recovery.PathParameters = map[string]string{"id": clientID}
	recovery.QueryStringParameters = map[string]string{"kind": "SUPPLIER", "tripId": tripID, "entityId": supplierID}
	response, err = (TreasuryAdminApp{Accounts: s}).HandleOperationRecovery(context.Background(), recovery)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("recover=%+v %v", response, err)
	}
	var envelope struct {
		Data OperationRecovery `json:"data"`
	}
	if json.Unmarshal([]byte(response.Body), &envelope) != nil {
		t.Fatal(response.Body)
	}
	if envelope.Data.Status != "APPLIED" || envelope.Data.Kind != "SUPPLIER" || envelope.Data.Supplier == nil {
		t.Fatalf("unexpected recovery: %+v", envelope.Data)
	}
}
