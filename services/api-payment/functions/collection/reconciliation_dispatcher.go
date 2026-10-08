package collection

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type reconciliationQueue interface {
	SendMessage(context.Context, *sqs.SendMessageInput, ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// ReconciliationDispatcherApp entrega en SQS únicamente trabajos persistidos.
type ReconciliationDispatcherApp struct {
	Queue    reconciliationQueue
	QueueURL string
	Now      func() time.Time
}

func (a ReconciliationDispatcherApp) Handle(ctx context.Context, event events.DynamoDBEvent) events.DynamoDBEventResponse {
	response := events.DynamoDBEventResponse{BatchItemFailures: []events.DynamoDBBatchItemFailure{}}
	for _, change := range event.Records {
		pk, pkOK := streamString(change.Change.NewImage, "pk")
		sk, skOK := streamString(change.Change.NewImage, "sk")
		attemptID, attemptOK := streamString(change.Change.NewImage, "attemptId")
		accountID, accountOK := streamString(change.Change.NewImage, "accountId")
		status, statusOK := streamString(change.Change.NewImage, "status")
		count, countOK := streamInt64(change.Change.NewImage, "count")
		if !countOK {
			count, countOK = 0, true
		}
		next, nextOK := streamInt64(change.Change.NewImage, "nextAttemptAt")
		if !pkOK || !skOK || !attemptOK || !accountOK || !statusOK || !countOK || !nextOK || pk != reconciliationPendingPK || status != "PENDING" || a.Queue == nil || a.QueueURL == "" || a.Now == nil {
			continue
		}
		body, err := json.Marshal(reconciliationMessage{AttemptID: attemptID, AccountID: accountID, JobSK: sk, Count: count})
		delay := next - a.Now().UTC().Unix()
		if delay < 0 {
			delay = 0
		}
		if delay > 900 {
			delay = 900
		}
		if err == nil {
			_, err = a.Queue.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(a.QueueURL), MessageBody: aws.String(string(body)), DelaySeconds: int32(delay)})
		}
		if err != nil {
			response.BatchItemFailures = append(response.BatchItemFailures, events.DynamoDBBatchItemFailure{ItemIdentifier: change.EventID})
		}
	}
	return response
}

func streamInt64(image map[string]events.DynamoDBAttributeValue, name string) (int64, bool) {
	value, ok := image[name]
	if !ok || value.DataType() != events.DataTypeNumber {
		return 0, false
	}
	number, err := strconv.ParseInt(value.Number(), 10, 64)
	return number, err == nil
}
