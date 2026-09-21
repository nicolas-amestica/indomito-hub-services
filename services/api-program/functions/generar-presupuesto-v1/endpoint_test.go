package generarpresupuestov1

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions/generar-presupuesto-v1/templates"
)

const testRequestID = "01JREQ00000000000000000000"

func TestHandleReturnsBase64PDFAndRequiredLogFields(t *testing.T) {
	request := budgetAPIRequest(t, validBudgetRequest())
	core, observed := observer.New(zapcore.DebugLevel)
	log := zap.New(core)

	response, err := handle(
		context.Background(),
		&functions.App{},
		log,
		request,
		templates.DefaultRegistry(),
		time.Date(2027, time.March, 4, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want %d: %s", response.StatusCode, http.StatusOK, response.Body)
	}
	if !response.IsBase64Encoded {
		t.Error("IsBase64Encoded = false, want true")
	}
	if response.Headers["Content-Type"] != contentTypePDF {
		t.Errorf("Content-Type = %q, want %q", response.Headers["Content-Type"], contentTypePDF)
	}
	document, err := base64.StdEncoding.DecodeString(response.Body)
	if err != nil {
		t.Fatalf("Body no es base64 válido: %v", err)
	}
	if len(document) < 5 || string(document[:5]) != "%PDF-" {
		t.Errorf("Body decodificado no tiene firma PDF")
	}

	entries := observed.FilterMessage("budgetGenerated").All()
	if len(entries) != 1 {
		t.Fatalf("logs budgetGenerated = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["budgetTemplateId"] != templates.BrochureDefaultID {
		t.Errorf("budgetTemplateId = %v", fields["budgetTemplateId"])
	}
	if fields["scenarioCount"] != int64(2) {
		t.Errorf("scenarioCount = %v, want 2", fields["scenarioCount"])
	}
	if fields["pdfBytes"] != int64(len(document)) {
		t.Errorf("pdfBytes = %v, want %d", fields["pdfBytes"], len(document))
	}
}

func TestHandleLogsValidationReasonWithoutPrices(t *testing.T) {
	invalid := validBudgetRequest()
	invalid.Scenarios[1].PricePerPassengerCLP = 0
	core, observed := observer.New(zapcore.DebugLevel)
	log := zap.New(core)

	response, err := handle(
		context.Background(),
		&functions.App{},
		log,
		budgetAPIRequest(t, invalid),
		templates.DefaultRegistry(),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("StatusCode = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}

	entries := observed.FilterMessage("generateBudgetRejected").All()
	if len(entries) != 1 {
		t.Fatalf("logs generateBudgetRejected = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["reason"] != reasonPriceNotPositive {
		t.Errorf("reason = %v, want %q", fields["reason"], reasonPriceNotPositive)
	}
	if fields["scenarioIndex"] != int64(2) {
		t.Errorf("scenarioIndex = %v, want 2", fields["scenarioIndex"])
	}
	if _, logged := fields["pricePerPassengerCLP"]; logged {
		t.Error("el rechazo registró el precio del escenario")
	}
}

func budgetAPIRequest(t *testing.T, body any) events.APIGatewayV2HTTPRequest {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return events.APIGatewayV2HTTPRequest{
		Body: string(encoded),
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			RequestID: testRequestID,
			Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
				Lambda: map[string]any{lambdautil.AuthorizerUserIDKey: "user-test"},
			},
		},
	}
}
