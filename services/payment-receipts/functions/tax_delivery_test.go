package functions

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type taxDeliveryDBFake struct {
	items   map[string]map[string]any
	updates int
}

func (f *taxDeliveryDBFake) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	pk := input.Key["pk"].(*types.AttributeValueMemberS).Value
	sk := input.Key["sk"].(*types.AttributeValueMemberS).Value
	item, _ := attributevalue.MarshalMap(f.items[pk+"/"+sk])
	return &dynamodb.GetItemOutput{Item: item}, nil
}

func (f *taxDeliveryDBFake) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

func (f *taxDeliveryDBFake) UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updates++
	return &dynamodb.UpdateItemOutput{}, nil
}

type taxDeliveryObjectsFake struct{ writes int }

func (f *taxDeliveryObjectsFake) PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.writes++
	return &s3.PutObjectOutput{}, nil
}

func (f *taxDeliveryObjectsFake) HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return &s3.HeadObjectOutput{}, nil
}

type taxDeliveryMailerFake struct {
	to    string
	sends int
}

func (f *taxDeliveryMailerFake) sendTax(_ context.Context, to string, _ int64, _ string, _ []byte) error {
	f.to = to
	f.sends++
	return nil
}

func TestRenderTaxDocumentCreatesPDFWithElectronicStamp(t *testing.T) {
	document, err := renderTaxDocument(taxPublicDocument{
		Folio: 87, IssueDate: "2026-11-02", IssuerRUT: "77654796-4", IssuerName: "GIRAS INDOMITO LIMITADA",
		IssuerActivity: "Turismo", IssuerAddress: "Dirección 123", IssuerCommune: "Santiago",
		ResolutionDate: "2026-11-02", ResolutionNumber: 0, NetAmount: 10000, VATAmount: 1900, TotalAmount: 11900,
		TED:   `<TED version="1.0"><DD><RE>77654796-4</RE><TD>39</TD><F>87</F></DD></TED>`,
		Items: []taxPublicItem{{Description: "Cuota de gira", Quantity: 1, UnitPrice: 11900}},
	})
	if err != nil {
		t.Fatalf("render tax document: %v", err)
	}
	if len(document) < 1000 || !bytes.HasPrefix(document, []byte("%PDF-")) {
		t.Fatal("invalid tax PDF")
	}
	if output := os.Getenv("TAX_PDF_SAMPLE_PATH"); output != "" {
		if err = os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			t.Fatalf("create sample directory: %v", err)
		}
		if err = os.WriteFile(output, document, 0o600); err != nil {
			t.Fatalf("write sample PDF: %v", err)
		}
	}
}

func TestTaxDocumentVisualFormatting(t *testing.T) {
	tests := []struct {
		name, input, expected string
		format                func(string) string
	}{
		{name: "date", input: "2026-11-05", expected: "05-11-2026", format: formatTaxDate},
		{name: "invalid date is preserved", input: "05/11/2026", expected: "05/11/2026", format: formatTaxDate},
		{name: "historical XML description preserved", input: "Pago 3 de gira 01M4HF0P3YMX9JA9QMDK34MRN7", expected: "Pago 3 de gira 01M4HF0P3YMX9JA9QMDK34MRN7", format: taxItemDescription},
		{name: "canonical XML description preserved", input: "Cuota N.º 7 Contrato terminado en DK34MRN7", expected: "Cuota N.º 7 Contrato terminado en DK34MRN7", format: taxItemDescription},
		{name: "unknown description is preserved", input: "Servicio especial", expected: "Servicio especial", format: taxItemDescription},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := test.format(test.input); actual != test.expected {
				t.Fatalf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

func TestRenderTaxDocumentWithFullLengthTED(t *testing.T) {
	model := validTaxPublicDocument()
	// Simula el tamaño de un TED firmado, sin usar datos ni firmas reales.
	model.TED = `<TED version="1.0"><DD><RE>77654796-4</RE><TD>39</TD><F>406</F><CAF>` + strings.Repeat("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", 20) + `</CAF></DD></TED>`
	before := model.TED
	document, err := renderTaxDocument(model)
	if err != nil || !bytes.HasPrefix(document, []byte("%PDF-")) {
		t.Fatalf("render full-length TED: %v", err)
	}
	if model.TED != before {
		t.Fatal("TED content changed")
	}
	width, height := taxStampSize(886, 116)
	if width > 460+1e-9 || height > 102+1e-9 || math.Abs(width/height-float64(886)/116) > 1e-9 {
		t.Fatalf("invalid stamp dimensions: %f x %f", width, height)
	}
}

func TestRenderTaxDocumentSupportsLongDescriptionsAndMultipleItems(t *testing.T) {
	model := validTaxPublicDocument()
	model.Items = make([]taxPublicItem, 32)
	for index := range model.Items {
		model.Items[index] = taxPublicItem{Description: strings.Repeat("Descripción extensa para comprobar ajuste automático ", 4), Quantity: 1, UnitPrice: 11900}
	}
	document, err := renderTaxDocument(model)
	if err != nil {
		t.Fatalf("render multipage tax document: %v", err)
	}
	if len(document) < 2000 || bytes.Count(document, []byte("/Type /Page")) < 2 {
		t.Fatal("expected a valid multipage tax PDF")
	}
}

func TestProcessTaxDeliveryUsesOptionalRequestEmail(t *testing.T) {
	requestID := "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	db := &taxDeliveryDBFake{items: map[string]map[string]any{
		"TAX_PUBLIC#39#87/META":              {"publicTaxDocument": validTaxPublicDocument()},
		"TAX_REQUEST#" + requestID + "/META": {"taxRequest": map[string]any{"deliveryEmail": "receptor@example.test"}},
	}}
	objects := &taxDeliveryObjectsFake{}
	mailer := &taxDeliveryMailerFake{}
	deps := taxDeliveryDependencies{db: db, table: "payments", objects: objects, bucket: "documents", mail: mailer, now: func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) }}
	err := processTaxDelivery(context.Background(), taxDeliveryJob{PK: "TAX_JOB#2026-11-04", SK: "PENDING#" + requestID, RequestID: requestID, Folio: 87}, deps)
	if err != nil || objects.writes != 1 || mailer.sends != 1 || mailer.to != "receptor@example.test" || db.updates != 1 {
		t.Fatalf("unexpected delivery: err=%v writes=%d sends=%d to=%q updates=%d", err, objects.writes, mailer.sends, mailer.to, db.updates)
	}
}

func TestProcessTaxDeliverySkipsEmailWhenOptionalAddressIsEmpty(t *testing.T) {
	requestID := "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	db := &taxDeliveryDBFake{items: map[string]map[string]any{
		"TAX_PUBLIC#39#87/META":              {"publicTaxDocument": validTaxPublicDocument()},
		"TAX_REQUEST#" + requestID + "/META": {"taxRequest": map[string]any{}},
	}}
	objects := &taxDeliveryObjectsFake{}
	mailer := &taxDeliveryMailerFake{}
	deps := taxDeliveryDependencies{db: db, table: "payments", objects: objects, bucket: "documents", mail: mailer, now: time.Now}
	err := processTaxDelivery(context.Background(), taxDeliveryJob{PK: "TAX_JOB#2026-11-04", SK: "PENDING#" + requestID, RequestID: requestID, Folio: 87}, deps)
	if err != nil || mailer.sends != 0 || db.updates != 1 {
		t.Fatalf("unexpected empty-email delivery: err=%v sends=%d updates=%d", err, mailer.sends, db.updates)
	}
}

func validTaxPublicDocument() taxPublicDocument {
	return taxPublicDocument{
		Folio: 87, IssueDate: "2026-11-04", IssuerRUT: "77654796-4", IssuerName: "GIRAS INDOMITO LIMITADA",
		IssuerActivity: "Turismo", IssuerAddress: "Dirección 123", IssuerCommune: "Santiago",
		ResolutionDate: "2026-11-02", TotalAmount: 11900,
		TED:   `<TED version="1.0"><DD><RE>77654796-4</RE><TD>39</TD><F>87</F></DD></TED>`,
		Items: []taxPublicItem{{Description: "Cuota de gira", Quantity: 1, UnitPrice: 11900}},
	}
}
