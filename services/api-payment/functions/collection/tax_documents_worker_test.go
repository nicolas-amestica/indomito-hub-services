package collection

import (
	"context"
	"fmt"
	"testing"
	"time"

	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"

	"github.com/aws/aws-lambda-go/events"
)

type taxConfigFake struct{ credentials sii.Credentials }

func (f taxConfigFake) Load(context.Context, time.Time) (sii.Credentials, error) {
	return f.credentials, nil
}

type taxIssuerFake struct {
	requests []TaxDocumentRequest
	folios   []int64
}

func (f *taxIssuerFake) Issue(_ context.Context, _ sii.Credentials, request TaxDocumentRequest, folio int64, _ time.Time) (TaxIssuanceResult, error) {
	f.requests = append(f.requests, request)
	f.folios = append(f.folios, folio)
	xml := []byte(`<?xml version="1.0" encoding="ISO-8859-1"?><DTE><Documento><Encabezado><IdDoc><Folio>` + fmt.Sprint(folio) + `</Folio><FchEmis>` + request.EffectiveDate + `</FchEmis></IdDoc><Totales><MntNeto>` + fmt.Sprint(request.GrossAmount) + `</MntNeto><IVA>0</IVA><MntTotal>` + fmt.Sprint(request.GrossAmount) + `</MntTotal></Totales></Encabezado><TED><DD><F>` + fmt.Sprint(folio) + `</F></DD></TED></Documento></DTE>`)
	return TaxIssuanceResult{Submission: sii.Submission{TrackID: "123456789", Status: taxStatusSubmitted}, Document: sii.Document{XML: xml, Folio: folio, IssuerRUT: "77654796-4", Amount: request.GrossAmount}}, nil
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
	public, err := service.read(context.Background(), "TAX_PUBLIC#39#1", "META")
	if err != nil || public.PublicTaxDocument == nil || public.PublicTaxDocument.TotalAmount != 287500 || public.PublicTaxDocument.TED == "" {
		t.Fatalf("public tax document was not materialized: %+v %v", public.PublicTaxDocument, err)
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

func TestTaxDocumentWorkerStartsAtNewCAFRangeAfterRotation(t *testing.T) {
	service, db := serviceFixture(t)
	now := time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)
	request := newManualSalesReceiptRequest("01M68000000000000000000070", testPaymentEvent(now, 29800))
	saveLookupRow(t, db, record{PK: "TAX_REQUEST#" + request.RequestID, SK: "META", TaxRequest: &request, Status: request.Status})
	saveLookupRow(t, db, record{PK: "TAX_QUEUE#PENDING", SK: request.RequestedAt.UTC().Format(time.RFC3339Nano) + "#" + request.RequestID, TaxRequest: &request, Status: request.Status})
	saveLookupRow(t, db, record{PK: "TAX_FOLIO#39", SK: "META", Count: 24, Status: "RESERVED"})
	issuer := &taxIssuerFake{}
	app := TaxDocumentWorkerApp{Accounts: service, Config: taxConfigFake{credentials: sii.Credentials{FolioFrom: 31, FolioTo: 80}}, Issuer: issuer, Now: func() time.Time { return now }}

	if err := app.Process(context.Background(), request.RequestID); err != nil {
		t.Fatalf("process rotated CAF: %v", err)
	}
	if len(issuer.folios) != 1 || issuer.folios[0] != 31 {
		t.Fatalf("expected first folio 31 from rotated CAF, got %v", issuer.folios)
	}
}

func TestTaxDocumentReissueLinksPublicFolios(t *testing.T) {
	service, db := serviceFixture(t)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	originalID := paymentOperationID("original-tax")
	replacementID := paymentOperationID("replacement-tax")
	original := TaxDocumentRequest{RequestID: originalID, Status: taxStatusRejected, SIIIssuance: &SIIIssuance{Folio: 11}, ReissuedBy: replacementID}
	replacement := TaxDocumentRequest{RequestID: replacementID, Status: taxStatusIssuing, RequestedAt: now.Add(-time.Minute), ReissuesRequest: originalID, SIIIssuance: &SIIIssuance{Folio: 22}}
	queueSK := replacement.RequestedAt.Format(time.RFC3339Nano) + "#" + replacementID
	for _, row := range []record{
		{PK: "TAX_REQUEST#" + originalID, SK: "META", TaxRequest: &original, Status: taxStatusRejected},
		{PK: "TAX_PUBLIC#39#11", SK: "META", PublicTaxDocument: &PublicTaxDocument{Folio: 11, SIIStatus: "REJECTED", ReplacementPending: true}, Status: taxStatusRejected},
		{PK: "TAX_REQUEST#" + replacementID, SK: "META", TaxRequest: &replacement, Status: taxStatusIssuing},
		{PK: "TAX_QUEUE#PENDING", SK: queueSK, TaxRequest: &replacement, Status: taxStatusReady},
	} {
		saveLookupRow(t, db, row)
	}
	public := PublicTaxDocument{Folio: 22, IssueDate: "2026-11-12", TotalAmount: 20000, SIIStatus: "PENDING", IssuedByIndomito: true}
	app := TaxDocumentWorkerApp{Accounts: service}
	if err := app.recordSubmission(context.Background(), replacement, sii.Submission{TrackID: "track-22"}, public, now); err != nil {
		t.Fatalf("record replacement: %v", err)
	}
	newProjection, _ := service.read(context.Background(), "TAX_PUBLIC#39#22", "META")
	oldProjection, _ := service.read(context.Background(), "TAX_PUBLIC#39#11", "META")
	if newProjection.PublicTaxDocument == nil || newProjection.PublicTaxDocument.OriginalFolio != 11 || oldProjection.PublicTaxDocument == nil || oldProjection.PublicTaxDocument.ReplacementFolio != 22 || oldProjection.PublicTaxDocument.ReplacementPending {
		t.Fatalf("public folio chain missing: original=%+v replacement=%+v", oldProjection.PublicTaxDocument, newProjection.PublicTaxDocument)
	}
}

func testPaymentEvent(now time.Time, amount int64) domain.Event {
	return domain.Event{Audit: domain.Audit{CommandID: approvedID, RecordedAt: now}, AccountID: "account", TripID: "trip", Amount: amount, EffectiveDate: now.Format(time.DateOnly)}
}

func TestTaxDocumentWorkerResumesReservedFolioWithoutAllocatingAnother(t *testing.T) {
	service, db := serviceFixture(t)
	now := time.Date(2026, 11, 5, 23, 0, 0, 0, time.UTC)
	request := newManualSalesReceiptRequest(approvedID, testPaymentEvent(now, 287500))
	queue := request
	request.Status = taxStatusIssuing
	request.SIIIssuance = &SIIIssuance{Folio: 405}
	saveLookupRow(t, db, record{PK: "TAX_REQUEST#" + request.RequestID, SK: "META", TaxRequest: &request, Status: request.Status})
	saveLookupRow(t, db, record{PK: "TAX_FOLIO#39", SK: "META", Count: 405, Status: "RESERVED"})
	saveLookupRow(t, db, record{PK: "TAX_QUEUE#PENDING", SK: queue.RequestedAt.UTC().Format(time.RFC3339Nano) + "#" + queue.RequestID, TaxRequest: &queue, Status: queue.Status})
	issuer := &taxIssuerFake{}
	app := TaxDocumentWorkerApp{Accounts: service, Config: taxConfigFake{credentials: sii.Credentials{FolioFrom: 400, FolioTo: 500}}, Issuer: issuer, Now: func() time.Time { return now }}
	for attempt := 0; attempt < 2; attempt++ {
		if err := app.Process(context.Background(), request.RequestID); err != nil {
			t.Fatalf("resume attempt %d: %v", attempt, err)
		}
	}
	if len(issuer.folios) != 1 || issuer.folios[0] != 405 {
		t.Fatalf("reserved folio was not reused exactly once: %v", issuer.folios)
	}
	counter, err := service.read(context.Background(), "TAX_FOLIO#39", "META")
	if err != nil || counter.Count != 405 {
		t.Fatalf("folio counter changed: %+v %v", counter, err)
	}
}
