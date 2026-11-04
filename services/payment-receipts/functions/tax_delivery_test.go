package functions

import (
	"bytes"
	"context"
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

func (f *taxDeliveryMailerFake) sendTax(_ context.Context, to string, _ int64, _ []byte) error {
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
