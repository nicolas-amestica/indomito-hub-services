package functions

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

const testReceiptID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

type fakeStore struct {
	row                               receiptRow
	claimed                           bool
	documents, sentCount, failedCount int
}

func (s *fakeStore) get(context.Context, string) (receiptRow, error)     { return s.row, nil }
func (s *fakeStore) claim(context.Context, string, string) (bool, error) { return s.claimed, nil }
func (s *fakeStore) documentReady(context.Context, string, string, string, string) error {
	s.documents++
	s.row.Status = statusDocumentReady
	return nil
}
func (s *fakeStore) sent(context.Context, string, string) error {
	s.sentCount++
	s.row.Status = statusSent
	return nil
}
func (s *fakeStore) failed(context.Context, string, string, string) error {
	s.failedCount++
	return nil
}
func (s *fakeStore) finishJob(context.Context, string, string, string, string) error { return nil }

type fakeDocuments struct{ writes int }

func (d *fakeDocuments) putImmutable(context.Context, renderedDocument) error { d.writes++; return nil }

type fakeMailer struct {
	sends int
	err   error
}

func (m *fakeMailer) send(context.Context, string, string, string, []byte, bool) error {
	m.sends++
	return m.err
}

func validReceiptRow() receiptRow {
	return receiptRow{ReceiptID: testReceiptID, ReceiptEmail: "payer@example.test", Status: "PENDING_DOCUMENT", Event: &financialEvent{Type: "PAYMENT_RECEIVED", AccountID: testReceiptID, TripID: testReceiptID, Amount: 20000, EffectiveDate: "2026-10-01", RecordedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}}
}

func TestRenderReceiptIsDeterministicAndPrivate(t *testing.T) {
	model, err := buildReceiptModel(validReceiptRow())
	if err != nil {
		t.Fatal(err)
	}
	first, err := renderReceipt(model)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderReceipt(model)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes, second.Bytes) || first.SHA256 != second.SHA256 {
		t.Fatal("el PDF debe ser determinista")
	}
	if !bytes.HasPrefix(first.Bytes, []byte("%PDF")) || first.Key != "receipts/"+testReceiptID+"/"+testReceiptID+"/v1.pdf" {
		t.Fatalf("documento inválido: %s", first.Key)
	}
}

func TestReceiptV2RequiresPassengerIdentity(t *testing.T) {
	row := validReceiptRow()
	row.DocumentVersion = 2
	if _, err := buildReceiptModel(row); err == nil {
		t.Fatal("v2 sin identidad debe fallar")
	}
	row.PassengerName, row.PassengerDocument = "Ana Prueba", "12.345.678-5"
	model, err := buildReceiptModel(row)
	if err != nil {
		t.Fatal(err)
	}
	if model.PassengerName != "Ana Prueba" || model.PassengerDocument != "12.345.678-5" {
		t.Fatal("identidad no preservada")
	}
}

func TestReceiptV3ContainsPermanentVerificationCode(t *testing.T) {
	t.Setenv("RECEIPT_VERIFICATION_URL", "https://pagos.dev.girasindomito.cl/verificar-comprobante")
	row := validReceiptRow()
	row.DocumentVersion = 3
	row.PassengerName, row.PassengerDocument = "Ana Prueba", "12.345.678-5"
	model, err := buildReceiptModel(row)
	if err != nil {
		t.Fatal(err)
	}
	document, err := renderReceipt(model)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(document.Bytes, []byte("%PDF")) || document.Key != "receipts/"+testReceiptID+"/"+testReceiptID+"/v3.pdf" {
		t.Fatalf("invalid v3 document: %s", document.Key)
	}
}

func TestProcessReceiptDoesNotDuplicateSentDelivery(t *testing.T) {
	store := &fakeStore{row: validReceiptRow(), claimed: true}
	docs := &fakeDocuments{}
	mail := &fakeMailer{}
	deps := dependencies{store: store, documents: docs, mail: mail}
	status, err := processReceipt(context.Background(), testReceiptID, deps)
	if err != nil || status != statusSent {
		t.Fatalf("status=%s err=%v", status, err)
	}
	status, err = processReceipt(context.Background(), testReceiptID, deps)
	if err != nil || status != statusSent {
		t.Fatalf("replay status=%s err=%v", status, err)
	}
	if docs.writes != 1 || mail.sends != 1 || store.sentCount != 1 {
		t.Fatalf("writes=%d sends=%d sent=%d", docs.writes, mail.sends, store.sentCount)
	}
}

func TestDocumentOnlyNeverSendsEmail(t *testing.T) {
	row := validReceiptRow()
	row.DeliveryMode = "DOCUMENT_ONLY"
	row.DocumentVersion = 2
	row.PassengerName = "Ana"
	row.PassengerDocument = "12.345.678-5"
	store := &fakeStore{row: row, claimed: true}
	docs := &fakeDocuments{}
	mail := &fakeMailer{}
	status, err := processReceipt(context.Background(), testReceiptID, dependencies{store: store, documents: docs, mail: mail})
	if err != nil || status != statusDocumentReady || mail.sends != 0 {
		t.Fatalf("status=%s sends=%d err=%v", status, mail.sends, err)
	}
}

func TestSMTPFailureUsesSafeFailureState(t *testing.T) {
	store := &fakeStore{row: validReceiptRow(), claimed: true}
	mail := &fakeMailer{err: errors.New("password=secret")}
	_, err := processReceipt(context.Background(), testReceiptID, dependencies{store: store, documents: &fakeDocuments{}, mail: mail})
	if err == nil || store.failedCount != 1 {
		t.Fatalf("err=%v failed=%d", err, store.failedCount)
	}
}

func TestGroupDepositUsesGroupPath(t *testing.T) {
	t.Setenv("RECEIPT_VERIFICATION_URL", "https://pagos.dev.girasindomito.cl/verificar-comprobante")
	row := validReceiptRow()
	row.Event.Type = "GROUP_DEPOSIT_RECEIVED"
	row.Event.AccountID = ""
	row.DocumentVersion = 3
	model, err := buildReceiptModel(row)
	if err != nil {
		t.Fatal(err)
	}
	document, err := renderReceipt(model)
	if err != nil {
		t.Fatal(err)
	}
	want := "receipts/groups/" + testReceiptID + "/" + testReceiptID + "/v3.pdf"
	if document.Key != want {
		t.Fatalf("key=%s want=%s", document.Key, want)
	}
}
