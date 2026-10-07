package collection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type taxStorageFake struct {
	objects map[string][]byte
}

func (f *taxStorageFake) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	content, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	f.objects[*input.Key] = content
	return &s3.PutObjectOutput{}, nil
}

func (f *taxStorageFake) HeadObject(_ context.Context, _ *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return &s3.HeadObjectOutput{}, nil
}

func TestManualTaxIssuancePreservesSourceAmountAndMovesQueueAtomically(t *testing.T) {
	service, db := serviceFixture(t)
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	requestID := approvedID
	tax := newManualSalesReceiptRequest(requestID, domain.Event{Audit: domain.Audit{CommandID: paymentOperationID("provider-confirmation"), RecordedAt: now}, AccountID: "account", TripID: "trip", Amount: 600000, EffectiveDate: "2026-10-07"})
	saveLookupRow(t, db, record{PK: "TAX_REQUEST#" + requestID, SK: "META", TaxRequest: &tax, Status: tax.Status, AccountID: tax.AccountID, TripID: tax.TripID})
	queueSK := tax.RequestedAt.Format(time.RFC3339Nano) + "#" + requestID
	saveLookupRow(t, db, record{PK: "TAX_QUEUE#PENDING", SK: queueSK, TaxRequest: &tax, Status: tax.Status})
	storage := &taxStorageFake{objects: map[string][]byte{}}
	app := TaxDocumentsApp{Accounts: service, Storage: storage, Bucket: "private-test", Now: func() time.Time { return now }}
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n%%EOF"))
	body := ManualTaxIssuanceRequest{CommandID: paymentOperationID("manual-tax-test"), Folio: "12345", IssueDate: "2026-10-07", Reason: "Boleta emitida manualmente en SII", PDFBase64: pdf}
	raw, _ := json.Marshal(body)
	req := events.APIGatewayV2HTTPRequest{Body: string(raw), PathParameters: map[string]string{"requestId": requestID}, RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "POST"}, Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]any{"userId": "operator", "paymentAccess": "admin"}}}}
	response, err := app.Handle(context.Background(), req)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("manual issuance failed: %+v %v", response, err)
	}
	stored, err := service.read(context.Background(), "TAX_REQUEST#"+requestID, "META")
	if err != nil || stored.TaxRequest == nil || stored.TaxRequest.Status != taxStatusManualRecorded || stored.TaxRequest.GrossAmount != tax.GrossAmount || stored.TaxRequest.ManualIssuance == nil || stored.TaxRequest.ManualIssuance.Folio != "12345" {
		t.Fatalf("unexpected canonical tax request: %+v %v", stored.TaxRequest, err)
	}
	if _, exists := db.items["TAX_QUEUE#PENDING/"+queueSK]; exists {
		t.Fatal("pending projection remained after the atomic transition")
	}
	if len(storage.objects) != 1 || db.items["TAX_FOLIO#BOLETA_VENTA_ELECTRONICA#12345/META"] == nil {
		t.Fatal("private document or unique folio reservation missing")
	}
	commits := db.commits
	response, err = app.Handle(context.Background(), req)
	if err != nil || response.StatusCode != 200 || db.commits != commits {
		t.Fatalf("idempotent replay changed state: %+v %v commits=%d", response, err, db.commits)
	}
}

func TestManualTaxIssuanceUsesDedicatedBoundedDecoder(t *testing.T) {
	body := `{"commandId":"` + approvedID + `","folio":"1","issueDate":"2026-10-07","reason":"Motivo valido","pdfBase64":"` + strings.Repeat("A", 70*1024) + `"}`
	var request ManualTaxIssuanceRequest
	if err := decodeManualTaxIssuance(events.APIGatewayV2HTTPRequest{Body: body}, &request); err != nil {
		t.Fatalf("body valid above the generic 64 KiB limit was rejected: %v", err)
	}
	tooLarge := strings.Repeat("A", maxManualTaxBodyBytes+1)
	if err := decodeManualTaxIssuance(events.APIGatewayV2HTTPRequest{Body: tooLarge}, &request); err == nil {
		t.Fatal("oversized tax body was accepted")
	}
}
