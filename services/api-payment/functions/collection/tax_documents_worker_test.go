package collection

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
)

type taxConfigFake struct{ credentials sii.Credentials }

func (f taxConfigFake) Load(context.Context, time.Time) (sii.Credentials, error) {
	return f.credentials, nil
}

type taxIssuerFake struct {
	requests []TaxDocumentRequest
	folios   []int64
}

func (f *taxIssuerFake) Issue(_ context.Context, _ sii.Credentials, request TaxDocumentRequest, folio int64, _ time.Time) (sii.Submission, error) {
	f.requests = append(f.requests, request)
	f.folios = append(f.folios, folio)
	return sii.Submission{TrackID: "123456789", Status: taxStatusSubmitted}, nil
}

func TestTaxDocumentWorkerReservesOneFolioAndPreservesReconciledAmount(t *testing.T) {
	service, db := serviceFixture(t)
	now := time.Date(2026, 10, 29, 20, 0, 0, 0, time.UTC)
	request := newManualSalesReceiptRequest(approvedID, testPaymentEvent(now, 287500))
	saveLookupRow(t, db, record{PK: "TAX_REQUEST#" + request.RequestID, SK: "META", TaxRequest: &request, Status: request.Status, AccountID: request.AccountID, TripID: request.TripID})
	queueSK := request.RequestedAt.UTC().Format(time.RFC3339Nano) + "#" + request.RequestID
	saveLookupRow(t, db, record{PK: "TAX_QUEUE#PENDING", SK: queueSK, TaxRequest: &request, Status: request.Status})
	issuer := &taxIssuerFake{}
	app := TaxDocumentWorkerApp{Accounts: service, Config: taxConfigFake{credentials: sii.Credentials{FolioFrom: 1, FolioTo: 30}}, Issuer: issuer, Now: func() time.Time { return now }}
	event := events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{{EventID: "event-1", Change: events.DynamoDBStreamRecord{NewImage: map[string]events.DynamoDBAttributeValue{
		"pk": events.NewStringAttribute("TAX_QUEUE#PENDING"), "status": events.NewStringAttribute(taxStatusReady), "taxRequest": events.NewMapAttribute(map[string]events.DynamoDBAttributeValue{"requestId": events.NewStringAttribute(request.RequestID)}),
	}}}}}
	result := app.Handle(context.Background(), event)
	if len(result.BatchItemFailures) != 0 {
		t.Fatalf("worker failed: %+v", result)
	}
	if len(issuer.requests) != 1 || issuer.requests[0].GrossAmount != 287500 || len(issuer.folios) != 1 || issuer.folios[0] != 1 {
		t.Fatalf("unexpected issue request: %+v folios=%v", issuer.requests, issuer.folios)
	}
	stored, err := service.read(context.Background(), "TAX_REQUEST#"+request.RequestID, "META")
	if err != nil || stored.TaxRequest == nil || stored.TaxRequest.Status != taxStatusSubmitted || stored.TaxRequest.SIIIssuance == nil || stored.TaxRequest.SIIIssuance.Folio != 1 || stored.TaxRequest.SIIIssuance.TrackID != "123456789" {
		t.Fatalf("unexpected stored request: %+v %v", stored.TaxRequest, err)
	}
	if _, exists := db.items["TAX_QUEUE#PENDING/"+queueSK]; exists {
		t.Fatal("tax queue entry was not removed")
	}
}

func TestTaxDocumentWorkerReservesNextFolioWhenCounterExists(t *testing.T) {
	service, db := serviceFixture(t)
	now := time.Date(2026, 10, 30, 22, 0, 0, 0, time.UTC)
	request := newManualSalesReceiptRequest("01M67QR2H4ASTR6TAZH1CRHFY7", testPaymentEvent(now, 2040))
	saveLookupRow(t, db, record{PK: "TAX_REQUEST#" + request.RequestID, SK: "META", TaxRequest: &request, Status: request.Status})
	saveLookupRow(t, db, record{PK: "TAX_QUEUE#PENDING", SK: request.RequestedAt.UTC().Format(time.RFC3339Nano) + "#" + request.RequestID, TaxRequest: &request, Status: request.Status})
	saveLookupRow(t, db, record{PK: "TAX_FOLIO#39", SK: "META", Count: 1, Status: "RESERVED"})
	issuer := &taxIssuerFake{}
	app := TaxDocumentWorkerApp{Accounts: service, Config: taxConfigFake{credentials: sii.Credentials{FolioFrom: 1, FolioTo: 30}}, Issuer: issuer, Now: func() time.Time { return now }}

	if err := app.Process(context.Background(), request.RequestID); err != nil {
		t.Fatalf("process next folio: %v", err)
	}
	if len(issuer.folios) != 1 || issuer.folios[0] != 2 {
		t.Fatalf("expected folio 2, got %v", issuer.folios)
	}
}

func testPaymentEvent(now time.Time, amount int64) domain.Event {
	return domain.Event{Audit: domain.Audit{CommandID: approvedID, RecordedAt: now}, AccountID: "account", TripID: "trip", Amount: amount, EffectiveDate: now.Format(time.DateOnly)}
}
