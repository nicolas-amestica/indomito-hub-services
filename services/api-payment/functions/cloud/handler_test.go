package cloud

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

type fakeDB struct {
	mu    sync.Mutex
	items map[string]map[string]types.AttributeValue
	count int
	fail  bool
}

func (d *fakeDB) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail {
		return nil, errors.New("storage")
	}
	return &dynamodb.GetItemOutput{Item: d.items[in.Key["pk"].(*types.AttributeValueMemberS).Value]}, nil
}
func (d *fakeDB) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail {
		return nil, errors.New("storage")
	}
	id := in.Item["pk"].(*types.AttributeValueMemberS).Value
	old := d.items[id]
	if *in.ConditionExpression == "attribute_not_exists(pk)" {
		if old != nil {
			return nil, &types.ConditionalCheckFailedException{}
		}
	} else if old == nil || old["status"].(*types.AttributeValueMemberS).Value != in.ExpressionAttributeValues[":previous"].(*types.AttributeValueMemberS).Value || old["owner"].(*types.AttributeValueMemberS).Value != in.ExpressionAttributeValues[":owner"].(*types.AttributeValueMemberS).Value {
		return nil, &types.ConditionalCheckFailedException{}
	}
	d.items[id] = in.Item
	return &dynamodb.PutItemOutput{}, nil
}
func (d *fakeDB) UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.count >= 10 {
		return nil, &types.ConditionalCheckFailedException{}
	}
	d.count++
	return &dynamodb.UpdateItemOutput{}, nil
}

type fakeGateway struct {
	mu                           sync.Mutex
	calls                        int
	failCreate, failVerify, live bool
}

func (g *fakeGateway) DevelopmentBank(context.Context) (string, error) {
	if g.live {
		return "", providers.ErrVerification
	}
	return "demo", nil
}
func (g *fakeGateway) Create(_ context.Context, in providers.CheckoutRequest) (providers.Checkout, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.failCreate {
		return providers.Checkout{}, providers.ErrProvider
	}
	return providers.Checkout{PaymentID: "abc123def456", PaymentURL: "https://khipu.com/payment/info/abc123def456"}, nil
}
func (g *fakeGateway) Verify(_ context.Context, id, ref string, amount int64) (providers.VerifiedPayment, error) {
	if g.failVerify {
		return providers.VerifiedPayment{}, providers.ErrVerification
	}
	return providers.VerifiedPayment{PaymentID: id, TransactionID: ref, Amount: strconv.FormatInt(amount, 10), ConciliationDate: time.Now().UTC()}, nil
}
func setup() (*App, *fakeDB, *fakeGateway) {
	d := &fakeDB{items: map[string]map[string]types.AttributeValue{}}
	g := &fakeGateway{}
	webhookSigningKeyFixture := "synthetic-webhook-key"
	return &App{DB: d, Gateway: g, Table: "test", BaseURL: "https://api.example", WebhookSecret: webhookSigningKeyFixture, Now: time.Now}, d, g
}
func req(id, owner string) events.APIGatewayV2HTTPRequest {
	r := events.APIGatewayV2HTTPRequest{Body: "{}", Headers: map[string]string{"Idempotency-Key": id}, PathParameters: map[string]string{"id": id}}
	if owner != "" {
		r.RequestContext.Authorizer = &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]any{"userId": owner}}
	}
	return r
}
func requireCode(t *testing.T, r events.APIGatewayV2HTTPResponse, want int) {
	t.Helper()
	if r.StatusCode != want {
		t.Fatalf("got %d want %d: %s", r.StatusCode, want, r.Body)
	}
}

func TestPaymentLifecycleAndIsolation(t *testing.T) {
	a, d, g := setup()
	ctx := context.Background()
	id := ulid.Make().String()
	r := req(id, "owner")
	requireCode(t, a.Handle(ctx, Create, req(id, "")), 401)
	requireCode(t, a.Handle(ctx, Configuration, r), 200)
	requireCode(t, a.Handle(ctx, Create, r), 201)
	requireCode(t, a.Handle(ctx, Create, r), 200)
	if g.calls != 1 {
		t.Fatal("duplicate provider creation")
	}
	requireCode(t, a.Handle(ctx, Read, req(id, "other")), 404)
	requireCode(t, a.Handle(ctx, Verify, req(id, "other")), 404)
	requireCode(t, a.Handle(ctx, Create, req(id, "other")), 409)
	requireCode(t, a.Handle(ctx, Verify, r), 200)
	v, err := a.get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if v.Receipt == nil || v.Event == nil || v.NotificationStatus != "PENDING_WORKER" {
		t.Fatal("missing atomic effects")
	}
	receipt := v.Receipt.ID
	requireCode(t, a.Handle(ctx, Verify, r), 200)
	v, err = a.get(ctx, id)
	if err != nil || v.Receipt.ID != receipt {
		t.Fatal("duplicate receipt")
	}
	requireCode(t, a.Handle(ctx, Read, r), 200)
	d.fail = true
	requireCode(t, a.Handle(ctx, Read, r), 503)
}

func TestFailClosedAndAmbiguousCreation(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		modify func(*App, *fakeDB, *fakeGateway, *events.APIGatewayV2HTTPRequest)
		code   int
	}{
		{"invalid key", func(_ *App, _ *fakeDB, _ *fakeGateway, r *events.APIGatewayV2HTTPRequest) {
			r.Headers["Idempotency-Key"] = "invalid"
		}, 400},
		{"amount from client", func(_ *App, _ *fakeDB, _ *fakeGateway, r *events.APIGatewayV2HTTPRequest) { r.Body = `{"amount":1}` }, 400},
		{"live bank", func(_ *App, _ *fakeDB, g *fakeGateway, _ *events.APIGatewayV2HTTPRequest) { g.live = true }, 503},
		{"quota", func(_ *App, d *fakeDB, _ *fakeGateway, _ *events.APIGatewayV2HTTPRequest) { d.count = 10 }, 429},
		{"storage", func(_ *App, d *fakeDB, _ *fakeGateway, _ *events.APIGatewayV2HTTPRequest) { d.fail = true }, 503},
		{"ambiguous", func(_ *App, _ *fakeDB, g *fakeGateway, _ *events.APIGatewayV2HTTPRequest) { g.failCreate = true }, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, d, g := setup()
			r := req(ulid.Make().String(), "owner")
			tc.modify(a, d, g, &r)
			requireCode(t, a.Handle(ctx, Create, r), tc.code)
		})
	}
	a, _, g := setup()
	g.failCreate = true
	r := req(ulid.Make().String(), "owner")
	requireCode(t, a.Handle(ctx, Create, r), 502)
	requireCode(t, a.Handle(ctx, Create, r), 200)
	if g.calls != 1 {
		t.Fatal("ambiguous POST retried")
	}
}

func signed(a *App, id string) events.APIGatewayV2HTTPRequest {
	body := `{"payment_id":"abc123def456","transaction_id":"` + id + `"}`
	stamp := strconv.FormatInt(a.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(a.WebhookSecret))
	mac.Write([]byte(stamp + "." + body))
	return events.APIGatewayV2HTTPRequest{Body: body, Headers: map[string]string{"x-khipu-signature": "t=" + stamp + ",s=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))}}
}
func TestWebhookSignatureAndConcurrentDelivery(t *testing.T) {
	a, _, g := setup()
	ctx := context.Background()
	id := ulid.Make().String()
	requireCode(t, a.Handle(ctx, Create, req(id, "owner")), 201)
	requireCode(t, a.Handle(ctx, Webhook, events.APIGatewayV2HTTPRequest{Body: "{}"}), 401)
	r := signed(a, id)
	bad := r
	bad.Body += " "
	requireCode(t, a.Handle(ctx, Webhook, bad), 401)
	g.failVerify = true
	requireCode(t, a.Handle(ctx, Webhook, r), 409)
	g.failVerify = false
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := a.Handle(ctx, Webhook, r)
			if got.StatusCode != 200 {
				t.Errorf("concurrent webhook %d", got.StatusCode)
			}
		}()
	}
	wg.Wait()
	v, err := a.get(ctx, id)
	if err != nil || v.Status != "CONFIRMED" {
		t.Fatal("not confirmed")
	}
	original := v.Receipt.ID
	r.IsBase64Encoded = true
	r.Body = base64.StdEncoding.EncodeToString([]byte(r.Body))
	requireCode(t, a.Handle(ctx, Webhook, r), 200)
	v, err = a.get(ctx, id)
	if err != nil || v.Receipt.ID != original {
		t.Fatal("duplicate receipt")
	}
	requireCode(t, a.Handle(ctx, Webhook, signed(a, ulid.Make().String())), 404)
	var payload map[string]any
	if err = json.Unmarshal([]byte(a.Handle(ctx, Webhook, r).Body), &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["receipt"]; ok {
		t.Fatal("webhook leaked receipt")
	}
}

func TestEntrypointRejectsAnonymousBeforeAWS(t *testing.T) {
	ctx := context.Background()
	for _, endpoint := range []Endpoint{Create, Read, Verify, Configuration} {
		res, err := Handler(endpoint)(ctx, req("", ""))
		if err != nil {
			t.Fatal(err)
		}
		requireCode(t, res, 401)
	}
	res, err := Handler(Return)(ctx, req("", ""))
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, res, 200)
}

func TestAdditionalBoundaries(t *testing.T) {
	a, d, g := setup()
	ctx := context.Background()
	id := ulid.Make().String()
	r := req(id, "owner")
	requireCode(t, a.Handle(ctx, Read, r), 404)
	requireCode(t, a.Handle(ctx, Read, req("invalid", "owner")), 400)
	requireCode(t, a.Handle(ctx, Endpoint("unknown"), r), 404)
	g.live = true
	requireCode(t, a.Handle(ctx, Configuration, r), 503)
	g.live = false
	requireCode(t, a.Handle(ctx, Create, r), 201)
	v, err := a.get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, a.confirm(ctx, v, "different-id"), 409)
	v.Status = "BLOCKED"
	requireCode(t, a.confirm(ctx, v, "abc123def456"), 409)
	v.Status = "CONFIRMED"
	requireCode(t, a.confirm(ctx, v, "different-id"), 409)
	v.Status = "PENDING"
	g.failVerify = true
	requireCode(t, a.confirm(ctx, v, "abc123def456"), 409)
	g.failVerify = false
	d.fail = true
	requireCode(t, a.Handle(ctx, Webhook, signed(a, id)), 503)
	d.fail = false
	requireCode(t, a.Handle(ctx, Webhook, events.APIGatewayV2HTTPRequest{Body: "!", IsBase64Encoded: true}), 400)
	large := events.APIGatewayV2HTTPRequest{Body: string(make([]byte, 91*1024))}
	requireCode(t, a.Handle(ctx, Webhook, large), 413)
	res, err := Handler(Webhook)(ctx, large)
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, res, 413)
	t.Setenv("APP_STAGE", "prd")
	appMu.Lock()
	instance = nil
	appMu.Unlock()
	if _, err = GetApp(ctx); err == nil {
		t.Fatal("production bootstrap accepted")
	}
	res, err = Handler(Configuration)(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, res, 503)
}
