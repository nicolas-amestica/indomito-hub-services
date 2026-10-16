package functions

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type realtimeDBFake struct {
	item    map[string]types.AttributeValue
	updated bool
}

func (f *realtimeDBFake) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: f.item}, nil
}
func (f *realtimeDBFake) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}
func (f *realtimeDBFake) UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updated = true
	return &dynamodb.UpdateItemOutput{}, nil
}
func (f *realtimeDBFake) TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

type gatewayFake struct{ calls int }

func (f *gatewayFake) PostToConnection(context.Context, *apigatewaymanagementapi.PostToConnectionInput, ...func(*apigatewaymanagementapi.Options)) (*apigatewaymanagementapi.PostToConnectionOutput, error) {
	f.calls++
	return &apigatewaymanagementapi.PostToConnectionOutput{}, nil
}

func TestRealtimePublisherCompletesEventWithoutSubscribers(t *testing.T) {
	item, err := attributevalue.MarshalMap(realtimeOutbox{EventID: "event", EventType: "conversation.updated", Stream: "list", Sequence: 1, Payload: map[string]any{"phone": "56912345678"}})
	if err != nil {
		t.Fatal(err)
	}
	item["state"] = &types.AttributeValueMemberS{Value: "pending"}
	db, gateway := &realtimeDBFake{item: item}, &gatewayFake{}
	publisher := RealtimePublisher{DB: db, Gateway: gateway, TableName: "table", Now: func() time.Time { return time.Unix(100, 0).UTC() }}
	if err = publisher.Publish(context.Background(), WorkMessage{PK: "WORK_DUE#OUTBOX#01", SK: "2026-10-14T12:00:00Z#event"}); err != nil {
		t.Fatal(err)
	}
	if !db.updated || gateway.calls != 0 {
		t.Fatalf("event must complete without posts: updated=%v calls=%d", db.updated, gateway.calls)
	}
}
