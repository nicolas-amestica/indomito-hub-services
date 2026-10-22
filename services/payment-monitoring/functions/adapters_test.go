package functions

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	gomail "github.com/wneessen/go-mail"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"strings"
	"testing"
	"time"
)

type mockDynamo struct {
	t                           *testing.T
	fail, conflict, releaseFail bool
	writes, deletes             int
	item                        map[string]types.AttributeValue
}

func (m *mockDynamo) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if !aws.ToBool(in.ConsistentRead) {
		m.t.Fatal("eventual read")
	}
	if m.fail {
		return nil, errors.New("secret")
	}
	return &dynamodb.GetItemOutput{Item: m.item}, nil
}
func (m *mockDynamo) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	if in.Item["pk"].(*types.AttributeValueMemberS).Value != monitorPK {
		m.t.Fatal("financial write")
	}
	if m.conflict {
		return nil, &types.ConditionalCheckFailedException{}
	}
	if m.fail {
		return nil, errors.New("secret")
	}
	m.writes++
	return &dynamodb.PutItemOutput{}, nil
}
func (m *mockDynamo) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	if !aws.ToBool(in.ConsistentRead) || aws.ToInt32(in.Limit) != 50 || aws.ToString(in.KeyConditionExpression) != "pk = :pk" {
		m.t.Fatal(in)
	}
	if m.fail {
		return nil, errors.New("secret")
	}
	return &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{}, LastEvaluatedKey: in.ExclusiveStartKey}, nil
}
func (m *mockDynamo) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	if aws.ToString(in.ConditionExpression) != "#owner = :owner" {
		m.t.Fatal("unconditional release")
	}
	if m.fail || m.releaseFail {
		return nil, errors.New("secret")
	}
	m.deletes++
	return &dynamodb.DeleteItemOutput{}, nil
}

type mockSSM struct {
	t                               *testing.T
	fail, plain, badConfig, badSMTP bool
}

func (m mockSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if !aws.ToBool(in.WithDecryption) {
		m.t.Fatal("no decrypt")
	}
	if m.fail {
		return nil, errors.New("secret")
	}
	value := `{"enabled":true,"email":"alerts@example.test","startMonth":"2026-01","pendingMinutes":30,"unsettledHours":72,"reminderHours":24,"maxPages":2}`
	if m.badConfig {
		value = `{}`
	}
	if strings.HasSuffix(aws.ToString(in.Name), "receipt-smtp") {
		value = `{"host":"smtp.example.test","port":587,"user":"test","password":"fixture","from":"alerts@example.test"}`
		if m.badSMTP {
			value = `{}`
		}
	}
	kind := ssmtypes.ParameterTypeSecureString
	if m.plain {
		kind = ssmtypes.ParameterTypeString
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String(value), Type: kind}}, nil
}

type mockSMTP struct {
	t      *testing.T
	fail   bool
	called bool
}

func (m *mockSMTP) DialAndSendWithContext(ctx context.Context, msgs ...*gomail.Msg) error {
	m.called = true
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 21*time.Second {
		m.t.Fatal("timeout absent")
	}
	if len(msgs) != 1 || len(msgs[0].GetTo()) != 1 {
		m.t.Fatal("recipient mismatch")
	}
	if m.fail {
		return errors.New("secret credentials")
	}
	return nil
}
func TestDynamoAdapters(t *testing.T) {
	ctx := context.Background()
	mock := &mockDynamo{t: t}
	db := dynamoStore{mock, "table"}
	if _, err := db.Get(ctx, "key", "meta"); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, item{PK: "TRIP#x"}); err == nil {
		t.Fatal("financial write accepted")
	}
	if err := db.Put(ctx, item{PK: monitorPK, SK: "test"}); err != nil {
		t.Fatal(err)
	}
	p, err := db.Query(ctx, "CASH#2026-10", map[string]string{"pk": "CASH#2026-10", "sk": "page2"})
	if err != nil || p.Next["sk"] != "page2" {
		t.Fatal(p, err)
	}
	if ok, err := db.acquire(ctx, "owner", testNow); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if err := db.release(ctx, "owner"); err != nil {
		t.Fatal(err)
	}
	mock.conflict = true
	if ok, err := db.acquire(ctx, "owner", testNow); ok || err != nil {
		t.Fatal(ok, err)
	}
	mock.conflict = false
	mock.fail = true
	if _, err := db.Get(ctx, "key", "meta"); err == nil {
		t.Fatal("read error hidden")
	}
	if err := db.Put(ctx, item{PK: monitorPK}); err == nil {
		t.Fatal("write error hidden")
	}
	if _, err := db.Query(ctx, "CASH#2026-10", nil); err == nil {
		t.Fatal("query error hidden")
	}
	if _, err := db.acquire(ctx, "owner", testNow); err == nil {
		t.Fatal("lease error hidden")
	}
	if err := db.release(ctx, "owner"); err == nil {
		t.Fatal("release error hidden")
	}
}
func TestParametersAndSMTP(t *testing.T) {
	for _, tc := range []struct {
		name, stage string
		mock        mockSSM
		fail        bool
	}{{"ok", "prd", mockSSM{t: t}, false}, {"stage", "qa", mockSSM{t: t}, true}, {"plain", "prd", mockSSM{t: t, plain: true}, true}, {"failure", "prd", mockSSM{t: t, fail: true}, true}} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parameter(context.Background(), tc.mock, tc.stage, "receipt-smtp")
			if (err != nil) != tc.fail {
				t.Fatal(err)
			}
		})
	}
	for _, port := range []int{465, 587, 25} {
		raw, err := json.Marshal(smtpConfig{"smtp.example.test", port, "user", "fixture", "sender@example.test"})
		if err != nil {
			t.Fatal(err)
		}
		c, err := parseSMTP(string(raw))
		if port == 25 {
			if err == nil {
				t.Fatal("plaintext accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		client, err := smtpClient(c)
		if err != nil {
			t.Fatal(err)
		}
		if port == 587 && client.TLSPolicy() != gomail.TLSMandatory.String() {
			t.Fatal("TLS optional")
		}
	}
	for _, raw := range []string{`{}`, `invalid`, `{"host":"smtp\r\n.example","port":587,"user":"u","password":"x","from":"a@example.test"}`} {
		if _, err := parseSMTP(raw); err == nil {
			t.Fatal("invalid SMTP accepted")
		}
	}
	fake := &mockSMTP{t: t}
	m := mailer{fake, "sender@example.test", "receiver@example.test", "prd"}
	if err := m.Send(context.Background(), "Subject", "Body"); err != nil {
		t.Fatal(err)
	}
	fake.fail = true
	if err := m.Send(context.Background(), "Subject", "Body"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("SMTP failure leaked or ignored")
	}
	m.from = "invalid"
	if err := m.Send(context.Background(), "s", "b"); err == nil {
		t.Fatal("invalid from")
	}
	m.from = "sender@example.test"
	m.to = "invalid"
	if err := m.Send(context.Background(), "s", "b"); err == nil {
		t.Fatal("invalid recipient")
	}
}
func TestMonitorHandler(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		fail, conflict, releaseFail bool
		wantErr                     bool
	}{{"ok", false, false, false, false}, {"lease-held", false, true, false, false}, {"db-fails", true, false, false, true}, {"release-fails", false, false, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockDynamo{t: t, fail: tc.fail, conflict: tc.conflict, releaseFail: tc.releaseFail}
			core, logs := observer.New(zap.InfoLevel)
			app := &App{db: dynamoStore{mock, "table"}, ssm: mockSSM{t: t}, stage: "prd", topic: "topic", log: zap.New(core)}
			err := app.Monitor(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatal(err)
			}
			if tc.name == "ok" {
				if mock.deletes != 1 || logs.Len() != 1 {
					t.Fatal("missing release or heartbeat")
				}
				entry := logs.All()[0].ContextMap()
				if entry["Stage"] != "prd" || entry["MonitorHeartbeat"] != int64(1) {
					t.Fatal(entry)
				}
			}
		})
	}
	for _, mock := range []mockSSM{{t: t, fail: true}, {t: t, badConfig: true}, {t: t, badSMTP: true}} {
		app := &App{ssm: mock, stage: "prd"}
		if err := app.Monitor(context.Background()); err == nil {
			t.Fatal("invalid dependencies accepted")
		}
	}
	// Un fallo de procesamiento debe soltar la concesión sin emitir heartbeat.
	bad, err := attributevalue.MarshalMap(item{Month: "2026-10", Kind: "INVALID"})
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockDynamo{t: t, item: bad}
	app := &App{db: dynamoStore{mock, "table"}, ssm: mockSSM{t: t}, stage: "prd", log: zap.NewNop()}
	if err := app.Monitor(context.Background()); err == nil || mock.deletes != 1 {
		t.Fatal("failed run left lease")
	}
}
func TestAlarmValidation(t *testing.T) {
	record := events.SNSEventRecord{EventSource: "aws:sns", SNS: events.SNSEntity{TopicArn: "topic", Message: `{"AlarmName":"khipu-renditions-prd-ReceiverErrors","NewStateValue":"ALARM"}`}}
	if _, _, err := alarmMessage(record, "prd", "topic", "us-east-1"); err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{`invalid`, `{"AlarmName":"khipu-renditions-dev-Test","NewStateValue":"ALARM"}`, `{"AlarmName":"khipu-renditions-prd-Test","NewStateValue":"OTHER"}`} {
		r := record
		r.SNS.Message = message
		if _, _, err := alarmMessage(r, "prd", "topic", "us-east-1"); err == nil {
			t.Fatal("invalid alarm accepted")
		}
	}
	app := &App{ssm: mockSSM{t: t}, stage: "prd", topic: "other"}
	if err := app.Alarm(context.Background(), events.SNSEvent{Records: []events.SNSEventRecord{record}}); err == nil {
		t.Fatal("untrusted topic")
	}
	app.topic = "topic"
	if err := app.Alarm(context.Background(), events.SNSEvent{}); err != nil {
		t.Fatal(err)
	}
	app.ssm = mockSSM{t: t, fail: true}
	if err := app.Alarm(context.Background(), events.SNSEvent{}); err == nil {
		t.Fatal("read failure hidden")
	}
}
func TestNewEnvironment(t *testing.T) {
	t.Setenv("APP_STAGE", "qa")
	if _, err := New(context.Background()); err == nil {
		t.Fatal("invalid environment")
	}
	t.Setenv("APP_STAGE", "dev")
	t.Setenv("PAYMENTS_TABLE_NAME", "table")
	t.Setenv("ALARM_TOPIC_ARN", "topic")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	if _, err := New(context.Background()); err != nil {
		t.Fatal(err)
	}
}
