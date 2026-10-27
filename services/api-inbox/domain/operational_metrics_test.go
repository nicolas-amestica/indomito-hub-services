package domain

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	costtypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/freetier"
	freetiertypes "github.com/aws/aws-sdk-go-v2/service/freetier/types"
)

type operationalMetricsDynamoFake struct {
	cache map[string]types.AttributeValue
	usage []map[string]types.AttributeValue
}

func (f *operationalMetricsDynamoFake) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: f.cache}, nil
}
func (f *operationalMetricsDynamoFake) PutItem(_ context.Context, input *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.cache = input.Item
	return &dynamodb.PutItemOutput{}, nil
}
func (f *operationalMetricsDynamoFake) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{Items: f.usage}, nil
}

type costExplorerFake struct{ calls int }

func (f *costExplorerFake) GetCostAndUsage(context.Context, *costexplorer.GetCostAndUsageInput, ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error) {
	f.calls++
	return &costexplorer.GetCostAndUsageOutput{ResultsByTime: []costtypes.ResultByTime{{Estimated: true, Total: map[string]costtypes.MetricValue{"UnblendedCost": {Amount: aws.String("0.1446"), Unit: aws.String("USD")}}}}}, nil
}

type freeTierFake struct{ calls int }

func (f *freeTierFake) GetFreeTierUsage(context.Context, *freetier.GetFreeTierUsageInput, ...func(*freetier.Options)) (*freetier.GetFreeTierUsageOutput, error) {
	f.calls++
	return &freetier.GetFreeTierUsageOutput{FreeTierUsages: []freetiertypes.FreeTierUsage{{Service: aws.String("AWS Lambda"), UsageType: aws.String("Request"), Unit: aws.String("Requests"), ActualUsageAmount: 700, ForecastedUsageAmount: 850, Limit: 1000}}}, nil
}

func TestOperationalMetricsAggregatesTokensAndCachesAWSForOneDay(t *testing.T) {
	usage, err := attributevalue.MarshalMap(agentUsageRecord{Calls: 2, InputTokens: 100, OutputTokens: 20, ThoughtTokens: 5, TotalTokens: 125, UpdatedAt: "2026-10-27T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	db := &operationalMetricsDynamoFake{usage: []map[string]types.AttributeValue{usage}}
	cost := &costExplorerFake{}
	free := &freeTierFake{}
	service, err := NewOperationalMetricsService(db, cost, free, "table")
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }

	first, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Agent.TotalTokens != 125 || first.Agent.Calls != 2 {
		t.Fatalf("unexpected agent metric: %+v", first.Agent)
	}
	if first.AWS.Cost.Amount != 0.1446 || first.AWS.Warning != 1 {
		t.Fatalf("unexpected AWS metric: %+v", first.AWS)
	}
	if second.AWS.Stale || cost.calls != 1 || free.calls != 1 {
		t.Fatalf("expected cached AWS result, cost=%d free=%d", cost.calls, free.calls)
	}
}
