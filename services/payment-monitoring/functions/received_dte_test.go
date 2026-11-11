package functions

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type metricFixture struct {
	t    *testing.T
	fail bool
}

func (f metricFixture) GetMetricStatistics(_ context.Context, in *cloudwatch.GetMetricStatisticsInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricStatisticsOutput, error) {
	if f.fail {
		return nil, errors.New("sensitive provider detail")
	}
	if aws.ToString(in.Namespace) != "AWS/Lambda" || len(in.Dimensions) != 1 || aws.ToString(in.Dimensions[0].Value) != "api-payment-prd-fn-sincronizar-dte-recibidos-v1" || aws.ToInt32(in.Period) != 300 {
		f.t.Fatal("unexpected metric scope")
	}
	value := float64(0)
	if aws.ToString(in.MetricName) == "Errors" {
		value = 2
	}
	return &cloudwatch.GetMetricStatisticsOutput{Datapoints: []cwtypes.Datapoint{{Sum: &value}}}, nil
}

type queueFixture struct{ fail bool }

func (f queueFixture) GetQueueAttributes(context.Context, *sqs.GetQueueAttributesInput, ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	if f.fail {
		return nil, errors.New("sensitive provider detail")
	}
	return &sqs.GetQueueAttributesOutput{Attributes: map[string]string{"ApproximateNumberOfMessages": "1", "ApproximateNumberOfMessagesNotVisible": "0"}}, nil
}

type probeFixture struct {
	health receivedHealth
	err    error
	calls  int
}

func (p *probeFixture) Read(context.Context, time.Time) (receivedHealth, error) {
	p.calls++
	return p.health, p.err
}

func TestReceivedDTEReadsExistingMetricsStateAndDLQ(t *testing.T) {
	row, err := attributevalue.MarshalMap(struct {
		State map[string]string `dynamodbav:"state"`
	}{map[string]string{"LastSuccess": testNow.Format(time.RFC3339)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                  string
		metricFail, queueFail bool
	}{{"healthy reads", false, false}, {"metric error", true, false}, {"queue error", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			probe := receivedAWSProbe{db: &mockDynamo{t: t, item: row}, metrics: metricFixture{t: t, fail: tc.metricFail}, queue: queueFixture{fail: tc.queueFail}, table: "table", queueURL: "queue", stage: "prd"}
			health, err := probe.Read(context.Background(), testNow)
			if tc.metricFail || tc.queueFail {
				if err == nil || strings.Contains(err.Error(), "sensitive") {
					t.Fatal("provider error ignored or leaked")
				}
				return
			}
			if err != nil || health.LastSuccess != testNow.Format(time.RFC3339) || health.Errors != 2 || health.DeadLetters != 1 {
				t.Fatalf("snapshot %+v %v", health, err)
			}
		})
	}
}

func TestReceivedDTEFindings(t *testing.T) {
	for _, tc := range []struct {
		name, last string
		errors     float64
		dlq        int64
		want       int
	}{
		{"fresh", testNow.Format(time.RFC3339), 0, 0, 0},
		{"stale", testNow.Add(-51 * time.Minute).Format(time.RFC3339), 0, 0, 1},
		{"missing", "", 0, 0, 1},
		{"errors and DLQ", testNow.Format(time.RFC3339), 1, 2, 2},
		{"invalid clock", testNow.Add(time.Hour).Format(time.RFC3339), 0, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(receivedFindings(receivedHealth{LastSuccess: tc.last, Errors: tc.errors, DeadLetters: tc.dlq}, testNow)); got != tc.want {
				t.Fatalf("findings %d want %d", got, tc.want)
			}
		})
	}
}

func TestReceivedDTEAlertReminderAndFailures(t *testing.T) {
	f := newFixture()
	probe := &probeFixture{health: receivedHealth{Errors: 1}}
	for _, at := range []time.Time{testNow, testNow.Add(30 * time.Minute), testNow.Add(61 * time.Minute)} {
		if err := runReceivedMonitor(context.Background(), probe, f, f, testConfig(), at); err != nil {
			t.Fatal(err)
		}
	}
	if f.sent != 2 {
		t.Fatalf("unexpected reminders: %d", f.sent)
	}
	probe.err = errors.New("read failure")
	if runReceivedMonitor(context.Background(), probe, f, f, testConfig(), testNow) == nil {
		t.Fatal("failed probe reported healthy")
	}
	c := testConfig()
	no := false
	c.Enabled = &no
	calls := probe.calls
	if err := runReceivedMonitor(context.Background(), probe, f, f, c, testNow); err != nil || probe.calls != calls {
		t.Fatal("disabled monitor performed reads")
	}
}

func TestReceivedDTEProbeFailureDoesNotEmitHealthyHeartbeat(t *testing.T) {
	db := &mockDynamo{t: t}
	core, logs := observer.New(zap.InfoLevel)
	app := &App{db: dynamoStore{db, "table"}, ssm: mockSSM{t: t}, stage: "prd", log: zap.New(core), received: &probeFixture{err: errors.New("read failure")}}
	if err := app.Monitor(context.Background()); err == nil || logs.Len() != 0 || db.deletes != 1 {
		t.Fatal("failed DTE probe must withhold heartbeat and release lease")
	}
}
