package functions

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type receivedHealth struct {
	LastSuccess       string
	Errors, Throttles float64
	DeadLetters       int64
}
type receivedProbe interface {
	Read(context.Context, time.Time) (receivedHealth, error)
}
type metricAPI interface {
	GetMetricStatistics(context.Context, *cloudwatch.GetMetricStatisticsInput, ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricStatisticsOutput, error)
}
type queueAPI interface {
	GetQueueAttributes(context.Context, *sqs.GetQueueAttributesInput, ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
}
type receivedAWSProbe struct {
	db                     dynamoAPI
	metrics                metricAPI
	queue                  queueAPI
	table, queueURL, stage string
}

func (p receivedAWSProbe) Read(ctx context.Context, now time.Time) (receivedHealth, error) {
	var result receivedHealth
	row, err := p.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &p.table, Key: key("DTE_INBOX#SYNC", "META"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return result, errors.New("DTE_MONITOR_READ_FAILED")
	}
	var state struct {
		State *struct{ LastSuccess string } `dynamodbav:"state"`
	}
	if err = attributevalue.UnmarshalMap(row.Item, &state); err != nil {
		return result, errors.New("DTE_MONITOR_STATE_INVALID")
	}
	if state.State != nil {
		result.LastSuccess = state.State.LastSuccess
	}
	for _, metric := range []struct {
		name  string
		value *float64
	}{{"Errors", &result.Errors}, {"Throttles", &result.Throttles}} {
		out, e := p.metrics.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{Namespace: aws.String("AWS/Lambda"), MetricName: &metric.name, Dimensions: []cwtypes.Dimension{{Name: aws.String("FunctionName"), Value: aws.String("api-payment-" + p.stage + "-fn-sincronizar-dte-recibidos-v1")}}, StartTime: aws.Time(now.Add(-20 * time.Minute)), EndTime: &now, Period: aws.Int32(300), Statistics: []cwtypes.Statistic{cwtypes.StatisticSum}})
		if e != nil || out == nil {
			return result, errors.New("DTE_MONITOR_METRICS_FAILED")
		}
		for _, point := range out.Datapoints {
			*metric.value += aws.ToFloat64(point.Sum)
		}
	}
	queue, err := p.queue.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{QueueUrl: &p.queueURL, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameApproximateNumberOfMessages, sqstypes.QueueAttributeNameApproximateNumberOfMessagesNotVisible}})
	if err != nil || queue == nil {
		return result, errors.New("DTE_MONITOR_QUEUE_FAILED")
	}
	for _, name := range []string{"ApproximateNumberOfMessages", "ApproximateNumberOfMessagesNotVisible"} {
		count, e := strconv.ParseInt(queue.Attributes[name], 10, 64)
		if e != nil || count < 0 {
			return result, errors.New("DTE_MONITOR_QUEUE_INVALID")
		}
		result.DeadLetters += count
	}
	return result, nil
}

func receivedFindings(health receivedHealth, now time.Time) []finding {
	var findings []finding
	last, err := time.Parse(time.RFC3339Nano, health.LastSuccess)
	if err != nil || last.After(now.Add(5*time.Minute)) || now.Sub(last) > 50*time.Minute {
		findings = append(findings, finding{"DTE_STALE", "received", "DTE recibidos: no hay una lectura exitosa confirmada durante los últimos 50 minutos. Revisar Gmail OAuth, la tarea programada y los logs del worker."})
	}
	if health.Errors > 0 || health.Throttles > 0 {
		findings = append(findings, finding{"DTE_ERRORS", "received", "DTE recibidos: hubo errores o límites de concurrencia durante los últimos 20 minutos. Revisar logs antes de reintentar."})
	}
	if health.DeadLetters > 0 {
		findings = append(findings, finding{"DTE_DLQ", "received", "DTE recibidos: hay solicitudes en la cola de errores. Corregir la causa y revisar su resultado antes de recuperarlas; no borrar los originales."})
	}
	return findings
}

func runReceivedMonitor(ctx context.Context, probe receivedProbe, db store, mail sender, c config, now time.Time) error {
	if !*c.Enabled {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	health, err := probe.Read(probeCtx, now)
	cancel()
	if err != nil {
		return err
	}
	// Evita correo cada quince minutos, sin esperar el recordatorio de rendiciones.
	c.ReminderHours = 1
	return notifyFindingsWithSubject(ctx, receivedFindings(health, now), db, mail, c, now, "DTE recibidos: revisión requerida")
}
