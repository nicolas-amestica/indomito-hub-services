package collection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestGroupReceiptListAndDownloadStayInsideTrip(t *testing.T) {
	db := &transactionDB{items: map[string]map[string]types.AttributeValue{}}
	service := Service{DB: db, Table: "payments"}
	signer := &receiptSignerFake{}
	app := ReceiptsApp{Accounts: service, Signer: signer}
	tripID, receiptID := approvedID, paymentOperationID("group-receipt")
	event := &domain.Event{TripID: tripID, Type: "GROUP_DEPOSIT_RECEIVED", Amount: 300000, EffectiveDate: "2026-10-05"}
	saveLookupRow(t, db, record{PK: "TRIP#" + tripID, SK: "RECEIPT#" + receiptID, TripID: tripID, ReceiptID: receiptID, Event: event})
	saveLookupRow(t, db, record{PK: "RECEIPT#" + receiptID, SK: "META", ReceiptID: receiptID, Event: event, DocumentKey: "receipts/groups/" + tripID + "/" + receiptID + "/v1.pdf", DocumentSHA256: strings.Repeat("a", 64)})
	req := annexAdminRequest("GET", tripID, "", nil)
	response := app.HandleGroupList(context.Background(), req)
	var body struct {
		Data ReceiptPage `json:"data"`
	}
	if json.Unmarshal([]byte(response.Body), &body) != nil || response.StatusCode != 200 || len(body.Data.Items) != 1 || body.Data.Items[0].Amount != 300000 {
		t.Fatalf("unexpected list: %s", response.Body)
	}
	req.PathParameters["id"] = receiptID
	if response = app.HandleGroupDownload(context.Background(), req); response.StatusCode != 200 || signer.calls != 1 {
		t.Fatalf("download blocked: %+v", response)
	}
	req.PathParameters["tripId"] = paymentOperationID("another-trip")
	if response = app.HandleGroupDownload(context.Background(), req); response.StatusCode < 400 || signer.calls != 1 {
		t.Fatal("cross-trip receipt disclosed")
	}
	req.RequestContext.Authorizer.Lambda["paymentAccess"] = "passenger"
	if app.HandleGroupList(context.Background(), req).StatusCode != 403 {
		t.Fatal("passenger listed administrative group receipts")
	}
}
