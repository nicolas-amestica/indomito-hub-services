package functions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
)

type realtimeDynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

type connectionPoster interface {
	PostToConnection(context.Context, *apigatewaymanagementapi.PostToConnectionInput, ...func(*apigatewaymanagementapi.Options)) (*apigatewaymanagementapi.PostToConnectionOutput, error)
}

type realtimeOutbox struct {
	EventID   string         `dynamodbav:"eventId" json:"eventId"`
	EventType string         `dynamodbav:"eventType" json:"type"`
	Stream    string         `dynamodbav:"stream" json:"stream"`
	Sequence  uint64         `dynamodbav:"sequence" json:"sequence"`
	Payload   map[string]any `dynamodbav:"payload" json:"payload"`
}

type realtimeEnvelope struct {
	realtimeOutbox
	OccurredAt time.Time `json:"occurredAt"`
}

type realtimeConnection struct {
	ConnectionID   string `dynamodbav:"connectionId"`
	SubscriptionPK string `dynamodbav:"subscriptionPk"`
}

// RealtimePublisher publishes an outbox item to every connection subscribed to its stream.
type RealtimePublisher struct {
	DB        realtimeDynamo
	Gateway   connectionPoster
	TableName string
	Now       func() time.Time
}

// Publish sends one durable event and marks it complete after all live recipients succeed.
func (p RealtimePublisher) Publish(ctx context.Context, work WorkMessage) error {
	if p.DB == nil || p.Gateway == nil || p.TableName == "" {
		return errors.New("realtime publisher configuration is required")
	}
	output, err := p.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: &p.TableName, Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: work.PK}, "sk": &types.AttributeValueMemberS{Value: work.SK}}, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return fmt.Errorf("read realtime outbox: %w", err)
	}
	if len(output.Item) == 0 {
		return nil
	}
	state, _ := output.Item["state"].(*types.AttributeValueMemberS)
	if state != nil && state.Value == "done" {
		return nil
	}
	var event realtimeOutbox
	if err = attributevalue.UnmarshalMap(output.Item, &event); err != nil || event.EventID == "" || event.Stream == "" || event.Sequence == 0 {
		return errors.New("invalid realtime outbox item")
	}
	occurredAt := p.Now().UTC()
	if prefix := strings.SplitN(work.SK, "#", 2)[0]; prefix != "" {
		if parsed, parseErr := time.Parse(time.RFC3339Nano, prefix); parseErr == nil {
			occurredAt = parsed
		}
	}
	body, err := json.Marshal(realtimeEnvelope{realtimeOutbox: event, OccurredAt: occurredAt})
	if err != nil {
		return fmt.Errorf("encode realtime event: %w", err)
	}
	partition := "WS_SUB#LIST"
	if strings.HasPrefix(event.Stream, "detail:") {
		partition = "WS_SUB#DETAIL#" + strings.TrimPrefix(event.Stream, "detail:")
	}
	connections, err := p.connections(ctx, partition)
	if err != nil {
		return err
	}
	for _, connection := range connections {
		_, postErr := p.Gateway.PostToConnection(ctx, &apigatewaymanagementapi.PostToConnectionInput{ConnectionId: aws.String(connection.ConnectionID), Data: body})
		if postErr == nil {
			continue
		}
		var apiErr smithy.APIError
		if errors.As(postErr, &apiErr) && apiErr.ErrorCode() == "GoneException" {
			if cleanupErr := p.removeConnection(ctx, connection); cleanupErr != nil {
				return cleanupErr
			}
			continue
		}
		return fmt.Errorf("publish realtime event: %w", postErr)
	}
	_, err = p.DB.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: &p.TableName, Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: work.PK}, "sk": &types.AttributeValueMemberS{Value: work.SK}}, UpdateExpression: aws.String("SET #state=:done, processedAt=:now"), ExpressionAttributeNames: map[string]string{"#state": "state"}, ExpressionAttributeValues: map[string]types.AttributeValue{":done": &types.AttributeValueMemberS{Value: "done"}, ":now": &types.AttributeValueMemberS{Value: p.Now().UTC().Format(time.RFC3339Nano)}}})
	if err != nil {
		return fmt.Errorf("complete realtime outbox: %w", err)
	}
	return nil
}

func (p RealtimePublisher) connections(ctx context.Context, partition string) ([]realtimeConnection, error) {
	var result []realtimeConnection
	var start map[string]types.AttributeValue
	for {
		output, err := p.DB.Query(ctx, &dynamodb.QueryInput{TableName: &p.TableName, KeyConditionExpression: aws.String("pk=:pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: partition}}, ExclusiveStartKey: start})
		if err != nil {
			return nil, fmt.Errorf("query realtime subscribers: %w", err)
		}
		var page []realtimeConnection
		if err = attributevalue.UnmarshalListOfMaps(output.Items, &page); err != nil {
			return nil, fmt.Errorf("decode realtime subscribers: %w", err)
		}
		result = append(result, page...)
		if len(output.LastEvaluatedKey) == 0 {
			return result, nil
		}
		start = output.LastEvaluatedKey
	}
}

func (p RealtimePublisher) removeConnection(ctx context.Context, connection realtimeConnection) error {
	_, err := p.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Delete: &types.Delete{TableName: &p.TableName, Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "WS_CONN#" + connection.ConnectionID}, "sk": &types.AttributeValueMemberS{Value: "META"}}}},
		{Delete: &types.Delete{TableName: &p.TableName, Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: connection.SubscriptionPK}, "sk": &types.AttributeValueMemberS{Value: "CONN#" + connection.ConnectionID}}}},
	}})
	return err
}

// ProcessRealtimeQueue reports only messages that must be retried.
func ProcessRealtimeQueue(ctx context.Context, publisher RealtimePublisher, event events.SQSEvent) events.SQSEventResponse {
	result := events.SQSEventResponse{BatchItemFailures: []events.SQSBatchItemFailure{}}
	for _, record := range event.Records {
		var work WorkMessage
		err := json.Unmarshal([]byte(record.Body), &work)
		if err == nil {
			err = publisher.Publish(ctx, work)
		}
		if err != nil {
			result.BatchItemFailures = append(result.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: record.MessageId})
		}
	}
	return result
}
