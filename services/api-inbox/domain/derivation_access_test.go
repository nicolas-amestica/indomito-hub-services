package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type accessDynamoFake struct {
	mu       sync.Mutex
	records  map[string]map[string]types.AttributeValue
	queries  []string
	messages []map[string]types.AttributeValue
	putErr   error
}

func accessPK(key map[string]types.AttributeValue) string {
	return key["pk"].(*types.AttributeValueMemberS).Value
}
func (f *accessDynamoFake) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &dynamodb.GetItemOutput{Item: f.records[accessPK(in.Key)]}, nil
}
func (f *accessDynamoFake) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return nil, f.putErr
	}
	f.records[accessPK(in.Item)] = in.Item
	return &dynamodb.PutItemOutput{}, nil
}
func (f *accessDynamoFake) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, in.ExpressionAttributeValues[":pk"].(*types.AttributeValueMemberS).Value)
	return &dynamodb.QueryOutput{Items: f.messages}, nil
}
func (f *accessDynamoFake) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	deletion := in.TransactItems[0].Delete
	key := accessPK(deletion.Key)
	if len(f.records[key]) == 0 {
		return nil, &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}
	}
	delete(f.records, key)
	item := in.TransactItems[1].Put.Item
	f.records[accessPK(item)] = item
	return &dynamodb.TransactWriteItemsOutput{}, nil
}
func newAccessFixture(t *testing.T) (*DerivationAccessService, *accessDynamoFake, *time.Time, string) {
	t.Helper()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	db := &accessDynamoFake{records: make(map[string]map[string]types.AttributeValue)}
	service, err := NewDerivationAccessService(db, "inbox", "https://nuevo.admin.girasindomito.cl")
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	link, err := service.CreateLink(context.Background(), DerivationNotification{SourcePhone: "56911112222", Alias: "Ana", ReasonLabel: "Solicita ayuda", DerivedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(link)
	token := strings.TrimPrefix(parsed.Fragment, "token=")
	if !derivationTokenPattern.MatchString(token) {
		t.Fatal("invalid token")
	}
	for key, item := range db.records {
		raw, _ := json.Marshal(item)
		if strings.Contains(key, token) || strings.Contains(string(raw), token) {
			t.Fatal("raw permission persisted")
		}
	}
	if parsed.RawQuery != "" || parsed.Path != "/derivaciones/56911112222" {
		t.Fatalf("unexpected link %s", link)
	}
	return service, db, &now, token
}
func TestDerivationAccessExpiresWithoutTTLDeletion(t *testing.T) {
	for _, elapsed := range []time.Duration{24 * time.Hour, 25 * time.Hour} {
		t.Run(elapsed.String(), func(t *testing.T) {
			service, db, now, token := newAccessFixture(t)
			*now = now.Add(elapsed)
			if _, err := service.Redeem(context.Background(), token); !errors.Is(err, ErrDerivationAccessInvalid) {
				t.Fatalf("expected expiry, got %v", err)
			}
			if len(db.records) != 1 {
				t.Fatal("test must retain expired TTL record")
			}
		})
	}
}
func TestDerivationAccessOneConcurrentRedeemerAndScopedRead(t *testing.T) {
	service, db, now, token := newAccessFixture(t)
	// Sigue disponible mucho después del plazo inicial de cinco minutos.
	*now = now.Add(23 * time.Hour)
	var wg sync.WaitGroup
	sessions := make(chan DerivationAccessSession, 12)
	failures := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, err := service.Redeem(context.Background(), token)
			if err != nil {
				failures <- err
			} else {
				sessions <- session
			}
		}()
	}
	wg.Wait()
	close(sessions)
	close(failures)
	if len(sessions) != 1 || len(failures) != 11 {
		t.Fatalf("redeemers succeeded=%d failed=%d", len(sessions), len(failures))
	}
	for err := range failures {
		if !errors.Is(err, ErrDerivationAccessInvalid) {
			t.Fatal(err)
		}
	}
	session := <-sessions
	sessionID := strings.TrimPrefix(accessPK(derivationAccessKey("SESSION", session.SessionToken)), "DERIVATION_SESSION#")
	if !session.ExpiresAt.Equal(now.Add(30 * time.Minute)) {
		t.Fatal("wrong session lifetime")
	}
	content := "No sé qué destino elegir"
	item, _ := attributevalue.MarshalMap(Message{ID: "1", Phone: "56911112222", Type: MessageTypeCustomer, Content: &content, Attachments: []MessageAttachment{{FileName: "audio.ogg", Bucket: "private", Key: "secret", DownloadURL: aws.String("https://private.example/secret")}}})
	db.messages = []map[string]types.AttributeValue{item}
	page, err := service.Read(context.Background(), sessionID, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Phone != "56911112222" || len(db.queries) != 1 || db.queries[0] != "CONV#56911112222" {
		t.Fatal("read escaped conversation scope")
	}
	raw, _ := json.Marshal(page)
	for _, secret := range []string{"private.example", "secret", "downloadUrl", "nonTerminalSends"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	if _, err = service.Redeem(context.Background(), session.SessionToken); !errors.Is(err, ErrDerivationAccessInvalid) {
		t.Fatal("session reused as link")
	}
	if _, err = service.Read(context.Background(), token, ""); !errors.Is(err, ErrDerivationAccessInvalid) {
		t.Fatal("link reused as session")
	}
	if _, err = service.Read(context.Background(), sessionID, "CONV#other"); !errors.Is(err, ErrDerivationAccessInvalid) {
		t.Fatal("unsafe pagination key")
	}
	*now = now.Add(30 * time.Minute)
	if _, err = service.Read(context.Background(), sessionID, ""); !errors.Is(err, ErrDerivationAccessInvalid) {
		t.Fatal("session did not expire")
	}
}
func TestDerivationAccessRejectsMalformedAndUnknownTokens(t *testing.T) {
	service, _, _, _ := newAccessFixture(t)
	for _, token := range []string{"", "x", strings.Repeat("a", 43), strings.Repeat("a", 5000), "../../other"} {
		if _, err := service.Redeem(context.Background(), token); !errors.Is(err, ErrDerivationAccessInvalid) {
			t.Fatalf("unexpected error %v", err)
		}
	}
}
func TestDerivationTemplateKeepsLinkWhenDetailIsLong(t *testing.T) {
	service, db, _, _ := newAccessFixture(t)
	sender := &derivationTemplateSenderFake{}
	notifier, err := NewWhatsAppDerivationNotifier(sender, DerivationNotificationConfig{Enabled: true, RecipientPhone: "56955556666", TemplateName: "alerta_derivacion_agente", Language: "es_CL"})
	if err != nil {
		t.Fatal(err)
	}
	notifier.SetAccessLinks(service)
	notification := DerivationNotification{SourcePhone: "56911112222", MessageID: "source", Detail: strings.Repeat("á", 2000), ReasonLabel: "Atención"}
	if err = notifier.NotifyDerivation(context.Background(), notification); err != nil {
		t.Fatal(err)
	}
	id := sender.clientMessageID
	detail := sender.template.Parameters[1]
	if len([]rune(detail)) > 900 || !strings.Contains(detail, "/derivaciones/56911112222#token=") || len(sender.template.Parameters) != 3 {
		t.Fatal("link truncated or template changed")
	}
	if err = notifier.NotifyDerivation(context.Background(), notification); err != nil {
		t.Fatal(err)
	}
	if sender.clientMessageID != id {
		t.Fatal("random link broke notification idempotency")
	}
	db.putErr = errors.New("storage unavailable")
	if err = notifier.NotifyDerivation(context.Background(), notification); err != nil {
		t.Fatal(err)
	}
	fallback := sender.template.Parameters[1]
	if strings.Contains(fallback, "#token=") || !strings.Contains(fallback, "requiere iniciar sesión") || !strings.Contains(fallback, "/comunicaciones/conversaciones?telefono=56911112222") {
		t.Fatal("missing authenticated fallback")
	}

}

type historyDynamoFake struct {
	agentStateDynamoFake
	pages []*dynamodb.QueryOutput
	limit int32
}

func (f *historyDynamoFake) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.limit = aws.ToInt32(in.Limit)
	if aws.ToString(in.KeyConditionExpression) != "pk = :pk AND sk BETWEEN :message AND :latest" {
		return nil, fmt.Errorf("history not bounded to derivation")
	}
	result := f.pages[0]
	f.pages = f.pages[1:]
	return result, nil
}
func TestDerivationHistoryIncludesTenOriginalMessagesAcrossPages(t *testing.T) {
	var items []map[string]types.AttributeValue
	for i := 12; i >= 0; i-- {
		role := MessageTypeCustomer
		if i%2 == 0 {
			role = MessageTypeAIAgent
		}
		if i == 11 {
			role = MessageTypeTeam
		}
		text := fmt.Sprintf("Mensaje %d", i)
		item, _ := attributevalue.MarshalMap(Message{ID: fmt.Sprint(i), Type: role, Content: &text})
		items = append(items, item)
	}
	db := &historyDynamoFake{pages: []*dynamodb.QueryOutput{{Items: items[:3], LastEvaluatedKey: snapshotKey("CONV#56911112222", "MSG#next")}, {Items: items[3:]}}}
	service := &AgentService{db: db, table: "inbox", cfg: AgentConfig{MaxHistoryTurns: 2}}
	history, err := service.derivationHistory(context.Background(), "56911112222", Message{ID: "12", MessageSK: "MSG#latest"})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 10 || history[0].Text != "Mensaje 2" || history[9].Text != "Mensaje 12" {
		t.Fatalf("wrong transcript %#v", history)
	}
	for _, message := range history {
		if message.Type == MessageTypeTeam {
			t.Fatal("included team message")
		}
	}
	if db.limit < 10 {
		t.Fatal("IA history configuration truncated notification")
	}
}
