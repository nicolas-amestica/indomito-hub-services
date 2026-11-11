package dteinbox

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
)

type memoryDB struct {
	rows map[string]map[string]types.AttributeValue
}

func memoryKey(attrs map[string]types.AttributeValue) string {
	return attrs["pk"].(*types.AttributeValueMemberS).Value + "|" + attrs["sk"].(*types.AttributeValueMemberS).Value
}
func (m *memoryDB) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: m.rows[memoryKey(in.Key)]}, nil
}
func (m *memoryDB) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	k := memoryKey(in.Item)
	if in.ConditionExpression != nil && m.rows[k] != nil {
		allowed := false
		if *in.ConditionExpression == "attribute_not_exists(pk) OR expiresAt < :now" {
			expires, _ := strconv.ParseInt(m.rows[k]["expiresAt"].(*types.AttributeValueMemberN).Value, 10, 64)
			now, _ := strconv.ParseInt(in.ExpressionAttributeValues[":now"].(*types.AttributeValueMemberN).Value, 10, 64)
			allowed = expires < now
		}
		if *in.ConditionExpression == "#job.#version = :version" {
			allowed = m.rows[k]["request"].(*types.AttributeValueMemberM).Value["version"].(*types.AttributeValueMemberN).Value == in.ExpressionAttributeValues[":version"].(*types.AttributeValueMemberN).Value
		}
		if !allowed {
			return nil, &types.ConditionalCheckFailedException{}
		}
	}
	m.rows[k] = in.Item
	return &dynamodb.PutItemOutput{}, nil
}

func (m *memoryDB) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	k := memoryKey(in.Key)
	if in.ConditionExpression != nil {
		current := m.rows[k]
		if current == nil || current["owner"].(*types.AttributeValueMemberS).Value != in.ExpressionAttributeValues[":owner"].(*types.AttributeValueMemberS).Value {
			return nil, &types.ConditionalCheckFailedException{}
		}
	}
	delete(m.rows, k)
	return &dynamodb.DeleteItemOutput{}, nil
}

func TestLeaseExcludesOverlapAndRecoversExpiredOwner(t *testing.T) {
	store := Store{DB: &memoryDB{rows: map[string]map[string]types.AttributeValue{}}, Table: "test"}
	ctx := context.Background()
	now := time.Now()
	if ok, err := store.AcquireLease(ctx, "first", now); err != nil || !ok {
		t.Fatalf("acquire %v %v", ok, err)
	}
	if ok, err := store.AcquireLease(ctx, "second", now.Add(time.Minute)); err != nil || ok {
		t.Fatalf("overlap %v %v", ok, err)
	}
	if ok, err := store.AcquireLease(ctx, "second", now.Add(7*time.Minute)); err != nil || !ok {
		t.Fatalf("expiry %v %v", ok, err)
	}
	if store.ReleaseLease(ctx, "first") == nil {
		t.Fatal("old owner deleted new lease")
	}
	if err := store.ReleaseLease(ctx, "second"); err != nil {
		t.Fatal(err)
	}
}

func (m *memoryDB) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}
func (m *memoryDB) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	for _, item := range in.TransactItems {
		if previous := m.rows[memoryKey(item.Put.Item)]; previous != nil {
			if item.Put.ConditionExpression != nil && *item.Put.ConditionExpression == "attribute_not_exists(pk) OR expiresAt < :now" {
				expires, _ := strconv.ParseInt(previous["expiresAt"].(*types.AttributeValueMemberN).Value, 10, 64)
				now, _ := strconv.ParseInt(item.Put.ExpressionAttributeValues[":now"].(*types.AttributeValueMemberN).Value, 10, 64)
				if expires < now {
					continue
				}
			}
			return nil, &types.ConditionalCheckFailedException{}
		}
	}
	for _, item := range in.TransactItems {
		m.rows[memoryKey(item.Put.Item)] = item.Put.Item
	}
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

type fakeMailbox struct {
	raw   string
	calls int
}

func (m *fakeMailbox) Connect(context.Context, GmailConfig) error { return nil }
func (m *fakeMailbox) List(context.Context, time.Time, string) (MessagePage, error) {
	var p MessagePage
	p.Messages = append(p.Messages, struct {
		ID string `json:"id"`
	}{ID: "mail-1"})
	return p, nil
}
func (m *fakeMailbox) Raw(context.Context, string) (string, error) { m.calls++; return m.raw, nil }

type fakeObjects struct {
	fail bool
	puts int
}

func (s *fakeObjects) PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	s.puts++
	if s.fail {
		return nil, errors.New("S3 unavailable")
	}
	return &s3.PutObjectOutput{}, nil
}

func TestWorkerRetriesWithoutAdvancingAndDeduplicates(t *testing.T) {
	raw := signedFixture(t)
	mime := "To: sii-dev@work.girasindomito.cl\r\nContent-Type: application/xml; name=dte.xml\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString(raw)
	db := &memoryDB{rows: map[string]map[string]types.AttributeValue{}}
	mail := &fakeMailbox{raw: base64.RawURLEncoding.EncodeToString([]byte(mime))}
	objects := &fakeObjects{fail: true}
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	worker := Worker{Mail: mail, Store: Store{DB: db, Table: "test"}, Objects: objects, Bucket: "test", Stage: "dev", Config: GmailConfig{Enabled: true, Mailbox: "agente@work.girasindomito.cl", Alias: "sii-dev@work.girasindomito.cl", StartDate: "2026-11-01"}, Credentials: sii.Credentials{Config: sii.Config{Environment: sii.EnvironmentCertification}, IssuerRUT: "76111111-1"}, Now: func() time.Time { return now }}
	ctx := context.Background()
	if worker.Run(ctx) == nil {
		t.Fatal("S3 failure must propagate")
	}
	state, _ := worker.Store.State(ctx)
	if state.LastSuccess != "" {
		t.Fatal("advanced before persistence")
	}
	objects.fail = false
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	state, _ = worker.Store.State(ctx)
	if state.LastSuccess == "" {
		t.Fatal("missing success watermark")
	}
	count := len(db.rows)
	calls := mail.calls
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(db.rows) != count || mail.calls != calls {
		t.Fatal("duplicate fetched or persisted twice")
	}
	// Los cambios de fecha no pueden duplicar una misma identidad tributaria.
	var original Document
	for _, attrs := range db.rows {
		var r row
		if err := attributevalue.UnmarshalMap(attrs, &r); err != nil {
			t.Fatal(err)
		}
		if r.Document != nil {
			original = *r.Document
			break
		}
	}
	original.IssueDate = "2026-10-31"
	original.SHA256 = "changed"
	if !errors.Is(worker.Store.SaveDocument(ctx, original), ErrDocumentConflict) {
		t.Fatal("identity conflict across months allowed")
	}
}

func TestWorkerRejectsCrossEnvironmentBeforeReading(t *testing.T) {
	mail := &fakeMailbox{}
	worker := Worker{Mail: mail, Stage: "dev", Config: GmailConfig{Enabled: true, Mailbox: "agente@work.girasindomito.cl", Alias: "sii@work.girasindomito.cl"}}
	if worker.Run(context.Background()) == nil || mail.calls != 0 {
		t.Fatal("cross environment allowed")
	}
}

func TestWorkerClassifiesSIIResponsesWithoutHidingInvalidDTE(t *testing.T) {
	for _, tc := range []struct{ name, xml, status string }{
		{"response", "<RESULTADO_ENVIO><ESTADO>0</ESTADO></RESULTADO_ENVIO>", "IGNORED"},
		{"malformed", "<RESULTADO_ENVIO>", "QUARANTINED"},
		{"unsigned DTE", "<RESULTADO_ENVIO><DTE><Documento/></DTE></RESULTADO_ENVIO>", "QUARANTINED"},
		{"unknown XML", "<Otro/>", "QUARANTINED"},
		{"honorarium", "<datos><tipodoc>66</tipodoc><numeroBoleta>21</numeroBoleta><totalHonorarios>1000</totalHonorarios></datos>", "QUARANTINED"},
		{"consumption report", `<ResultadoConsumoFolios xmlns="http://www.sii.cl/SiiDte"><Resultado><Estado>REPARO</Estado></Resultado></ResultadoConsumoFolios>`, "QUARANTINED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mime := "To: sii-dev@work.girasindomito.cl\r\nContent-Type: application/xml; name=respuesta.xml\r\n\r\n" + tc.xml
			db := &memoryDB{rows: map[string]map[string]types.AttributeValue{}}
			objects := &fakeObjects{}
			worker := Worker{Mail: &fakeMailbox{raw: base64.RawURLEncoding.EncodeToString([]byte(mime))}, Store: Store{DB: db, Table: "test"}, Objects: objects, Bucket: "test", Stage: "dev", Config: GmailConfig{Enabled: true, Mailbox: "agente@work.girasindomito.cl", Alias: "sii-dev@work.girasindomito.cl", StartDate: "2026-11-01"}, Credentials: sii.Credentials{Config: sii.Config{Environment: sii.EnvironmentCertification}}, Now: time.Now}
			if err := worker.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			var result row
			if err := attributevalue.UnmarshalMap(db.rows["DTE_MAIL#mail-1|META"], &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != tc.status {
				t.Fatalf("status %s, expected %s", result.Status, tc.status)
			}
			if (tc.name == "honorarium" || tc.name == "consumption report") && result.Detail == "XML o firma inválidos; revisar original" {
				t.Fatal("known non-DTE format must not be reported as an invalid signature")
			}
			if tc.status == "IGNORED" && db.rows["DTE_INBOX#ISSUES|mail-1"] != nil {
				t.Fatal("response created an incident")
			}
			if objects.puts != 2 {
				t.Fatal("original mail and XML must be preserved")
			}
		})
	}
}

func TestActionCommandCannotBeSentTwice(t *testing.T) {
	store := Store{DB: &memoryDB{rows: map[string]map[string]types.AttributeValue{}}, Table: "test"}
	ctx := context.Background()
	if err := store.ClaimAction(ctx, "document", "command", "operator", "ERM", "Recibido conforme"); err != nil {
		t.Fatal(err)
	}
	if store.ClaimAction(ctx, "document", "command", "operator", "ERM", "Recibido conforme") == nil {
		t.Fatal("same command could be sent twice")
	}
	if err := store.FinishAction(ctx, "document", "command", "operator", "ERM", "Recibido conforme", map[string]int{"code": 0}); err != nil {
		t.Fatal(err)
	}
	if store.ClaimAction(ctx, "document", "command", "operator", "ERM", "Recibido conforme") == nil {
		t.Fatal("completed command could be sent twice")
	}
}
