package collection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestPublicTaxDocumentVerificationRequiresExactFolioDateAndAmount(t *testing.T) {
	service, db := serviceFixture(t)
	document := PublicTaxDocument{DocumentType: 39, Folio: 381, IssueDate: "2026-11-02", IssuerRUT: "77654796-4", IssuerName: "GIRAS INDOMITO LIMITADA", TotalAmount: 29800, TED: "<TED/>"}
	saveLookupRow(t, db, record{PK: "TAX_PUBLIC#39#381", SK: "META", PublicTaxDocument: &document, Status: taxStatusSubmitted})
	app := PublicApp{Accounts: service, LookupSecret: strings.Repeat("test-only-", 4)}
	request := events.APIGatewayV2HTTPRequest{Body: `{"folio":"381","issueDate":"2026-11-02","amount":29800}`, RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "POST", SourceIP: "198.51.100.4"}}}
	response, err := app.HandleTaxDocumentVerification(context.Background(), request)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("unexpected response: %+v %v", response, err)
	}
	var envelope struct {
		Data PublicTaxDocument `json:"data"`
	}
	if json.Unmarshal([]byte(response.Body), &envelope) != nil || !envelope.Data.Authentic || envelope.Data.Folio != 381 || envelope.Data.TED == "" {
		t.Fatalf("unexpected document: %+v", envelope.Data)
	}
	request.Body = `{"folio":"381","issueDate":"2026-11-02","amount":1}`
	response, err = app.HandleTaxDocumentVerification(context.Background(), request)
	if err != nil || json.Unmarshal([]byte(response.Body), &envelope) != nil || envelope.Data.Authentic {
		t.Fatalf("mismatched amount leaked a document: %+v %v", envelope.Data, err)
	}
}
