package domain

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	costtypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/freetier"
)

const operationalMetricsCacheDuration = 24 * time.Hour

type operationalMetricsDynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

type costExplorerReader interface {
	GetCostAndUsage(context.Context, *costexplorer.GetCostAndUsageInput, ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error)
}

type freeTierReader interface {
	GetFreeTierUsage(context.Context, *freetier.GetFreeTierUsageInput, ...func(*freetier.Options)) (*freetier.GetFreeTierUsageOutput, error)
}

type AgentUsageMetric struct {
	Period        string `json:"period"`
	Calls         int64  `json:"calls"`
	InputTokens   int64  `json:"inputTokens"`
	OutputTokens  int64  `json:"outputTokens"`
	ThoughtTokens int64  `json:"thoughtTokens"`
	ToolUseTokens int64  `json:"toolUseTokens"`
	CachedTokens  int64  `json:"cachedTokens"`
	TotalTokens   int64  `json:"totalTokens"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

type AWSCostMetric struct {
	Amount    float64 `json:"amount" dynamodbav:"amount"`
	Currency  string  `json:"currency" dynamodbav:"currency"`
	Estimated bool    `json:"estimated" dynamodbav:"estimated"`
	Scope     string  `json:"scope" dynamodbav:"scope"`
	Period    string  `json:"period" dynamodbav:"period"`
}

type FreeTierMetric struct {
	Service             string  `json:"service" dynamodbav:"service"`
	UsageType           string  `json:"usageType" dynamodbav:"usageType"`
	Unit                string  `json:"unit" dynamodbav:"unit"`
	Actual              float64 `json:"actual" dynamodbav:"actual"`
	Forecast            float64 `json:"forecast" dynamodbav:"forecast"`
	Limit               float64 `json:"limit" dynamodbav:"limit"`
	Utilization         float64 `json:"utilization" dynamodbav:"utilization"`
	ForecastUtilization float64 `json:"forecastUtilization" dynamodbav:"forecastUtilization"`
	Status              string  `json:"status" dynamodbav:"status"`
}

type AWSOperationalMetric struct {
	Cost        AWSCostMetric    `json:"cost" dynamodbav:"cost"`
	FreeTier    []FreeTierMetric `json:"freeTier" dynamodbav:"freeTier"`
	Safe        int              `json:"safe" dynamodbav:"safe"`
	Warning     int              `json:"warning" dynamodbav:"warning"`
	Exceeded    int              `json:"exceeded" dynamodbav:"exceeded"`
	Available   bool             `json:"available" dynamodbav:"available"`
	FetchedAt   string           `json:"fetchedAt" dynamodbav:"fetchedAt"`
	Stale       bool             `json:"stale,omitempty" dynamodbav:"-"`
	WarningText string           `json:"warningText,omitempty" dynamodbav:"-"`
}

type OperationalMetrics struct {
	Agent AgentUsageMetric     `json:"agent"`
	AWS   AWSOperationalMetric `json:"aws"`
}

type awsOperationalCache struct {
	PK        string               `dynamodbav:"pk"`
	SK        string               `dynamodbav:"sk"`
	Metrics   AWSOperationalMetric `dynamodbav:"metrics"`
	ExpiresAt int64                `dynamodbav:"expiresAt"`
}

type agentUsageRecord struct {
	Calls         int64  `dynamodbav:"calls"`
	InputTokens   int64  `dynamodbav:"inputTokens"`
	OutputTokens  int64  `dynamodbav:"outputTokens"`
	ThoughtTokens int64  `dynamodbav:"thoughtTokens"`
	ToolUseTokens int64  `dynamodbav:"toolUseTokens"`
	CachedTokens  int64  `dynamodbav:"cachedTokens"`
	TotalTokens   int64  `dynamodbav:"totalTokens"`
	UpdatedAt     string `dynamodbav:"updatedAt"`
}

type OperationalMetricsService struct {
	db       operationalMetricsDynamo
	cost     costExplorerReader
	freeTier freeTierReader
	table    string
	now      func() time.Time
}

func NewOperationalMetricsService(db operationalMetricsDynamo, cost costExplorerReader, freeTier freeTierReader, table string) (*OperationalMetricsService, error) {
	if db == nil || cost == nil || freeTier == nil || table == "" {
		return nil, errors.New("operational metrics configuration is incomplete")
	}
	return &OperationalMetricsService{db: db, cost: cost, freeTier: freeTier, table: table, now: time.Now}, nil
}

func (s *OperationalMetricsService) Get(ctx context.Context) (OperationalMetrics, error) {
	agent, err := s.agentUsage(ctx)
	if err != nil {
		return OperationalMetrics{}, err
	}
	awsMetric, err := s.awsMetrics(ctx)
	if err != nil {
		return OperationalMetrics{}, err
	}
	return OperationalMetrics{Agent: agent, AWS: awsMetric}, nil
}

func (s *OperationalMetricsService) agentUsage(ctx context.Context) (AgentUsageMetric, error) {
	now := s.now().UTC()
	period := now.Format("2006-01")
	output, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.table),
		KeyConditionExpression: aws.String("pk = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: "AGENT_USAGE#" + period},
		},
		ConsistentRead: aws.Bool(false),
	})
	if err != nil {
		return AgentUsageMetric{}, fmt.Errorf("query agent usage: %w", err)
	}
	metric := AgentUsageMetric{Period: period}
	for _, item := range output.Items {
		var record agentUsageRecord
		if err = attributevalue.UnmarshalMap(item, &record); err != nil {
			return AgentUsageMetric{}, fmt.Errorf("decode agent usage: %w", err)
		}
		metric.Calls += record.Calls
		metric.InputTokens += record.InputTokens
		metric.OutputTokens += record.OutputTokens
		metric.ThoughtTokens += record.ThoughtTokens
		metric.ToolUseTokens += record.ToolUseTokens
		metric.CachedTokens += record.CachedTokens
		metric.TotalTokens += record.TotalTokens
		if record.UpdatedAt > metric.UpdatedAt {
			metric.UpdatedAt = record.UpdatedAt
		}
	}
	return metric, nil
}

func (s *OperationalMetricsService) awsMetrics(ctx context.Context) (AWSOperationalMetric, error) {
	now := s.now().UTC()
	cached, found, err := s.readAWSCache(ctx)
	if err != nil {
		return AWSOperationalMetric{}, err
	}
	if found && cached.ExpiresAt > now.Unix() {
		return cached.Metrics, nil
	}
	fresh, refreshErr := s.fetchAWSMetrics(ctx, now)
	if refreshErr != nil {
		if found {
			cached.Metrics.Stale = true
			cached.Metrics.WarningText = "AWS no pudo actualizar las metricas; se muestra el ultimo dato disponible."
			return cached.Metrics, nil
		}
		return AWSOperationalMetric{Available: false, WarningText: "AWS no pudo entregar las metricas operacionales."}, nil
	}
	if err = s.writeAWSCache(ctx, fresh, now); err != nil {
		// El dato ya fue obtenido; una falla de cache no debe ocultarlo.
		fresh.WarningText = "Las metricas se obtuvieron, pero no pudieron almacenarse en cache."
	}
	return fresh, nil
}

func (s *OperationalMetricsService) readAWSCache(ctx context.Context) (awsOperationalCache, bool, error) {
	output, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.table), Key: snapshotKey("OPERATIONAL_METRICS", "AWS")})
	if err != nil {
		return awsOperationalCache{}, false, fmt.Errorf("read operational metrics cache: %w", err)
	}
	if len(output.Item) == 0 {
		return awsOperationalCache{}, false, nil
	}
	var record awsOperationalCache
	if err = attributevalue.UnmarshalMap(output.Item, &record); err != nil {
		return awsOperationalCache{}, false, fmt.Errorf("decode operational metrics cache: %w", err)
	}
	return record, true, nil
}

func (s *OperationalMetricsService) writeAWSCache(ctx context.Context, metric AWSOperationalMetric, now time.Time) error {
	record, err := attributevalue.MarshalMap(awsOperationalCache{PK: "OPERATIONAL_METRICS", SK: "AWS", Metrics: metric, ExpiresAt: now.Add(operationalMetricsCacheDuration).Unix()})
	if err != nil {
		return err
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.table), Item: record})
	return err
}

func (s *OperationalMetricsService) fetchAWSMetrics(ctx context.Context, now time.Time) (AWSOperationalMetric, error) {
	start := now.Format("2006-01-01")
	end := now.AddDate(0, 0, 1).Format("2006-01-02")
	costOutput, err := s.cost.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
		TimePeriod:  &costtypes.DateInterval{Start: aws.String(start), End: aws.String(end)},
		Granularity: costtypes.GranularityMonthly,
		Metrics:     []string{"UnblendedCost"},
		GroupBy:     []costtypes.GroupDefinition{{Type: costtypes.GroupDefinitionTypeTag, Key: aws.String("Project")}},
	})
	if err != nil {
		return AWSOperationalMetric{}, fmt.Errorf("get AWS cost: %w", err)
	}
	costMetric := AWSCostMetric{Currency: "USD", Scope: "account", Period: now.Format("2006-01")}
	if len(costOutput.ResultsByTime) > 0 {
		result := costOutput.ResultsByTime[0]
		costMetric.Estimated = result.Estimated
		if value, ok := result.Total["UnblendedCost"]; ok {
			costMetric.Currency = aws.ToString(value.Unit)
			costMetric.Amount, _ = strconv.ParseFloat(aws.ToString(value.Amount), 64)
		}
		var accountAmount float64
		for _, group := range result.Groups {
			value, ok := group.Metrics["UnblendedCost"]
			if !ok {
				continue
			}
			amount, parseErr := strconv.ParseFloat(aws.ToString(value.Amount), 64)
			if parseErr != nil {
				continue
			}
			accountAmount += amount
			if len(group.Keys) > 0 && group.Keys[0] == "Project$indomito-hub" {
				costMetric.Amount = amount
				costMetric.Currency = aws.ToString(value.Unit)
				costMetric.Scope = "project"
			}
		}
		if costMetric.Scope == "account" && accountAmount > 0 {
			costMetric.Amount = accountAmount
		}
	}
	freeItems, err := s.allFreeTierUsage(ctx)
	if err != nil {
		return AWSOperationalMetric{}, fmt.Errorf("get AWS Free Tier usage: %w", err)
	}
	metric := AWSOperationalMetric{Cost: costMetric, Available: true, FetchedAt: now.Format(time.RFC3339)}
	for _, item := range freeItems {
		entry := FreeTierMetric{Service: aws.ToString(item.Service), UsageType: aws.ToString(item.UsageType), Unit: aws.ToString(item.Unit), Actual: item.ActualUsageAmount, Forecast: item.ForecastedUsageAmount, Limit: item.Limit}
		if entry.Limit > 0 {
			entry.Utilization = entry.Actual / entry.Limit * 100
			entry.ForecastUtilization = entry.Forecast / entry.Limit * 100
		}
		maxUsage := entry.Utilization
		if entry.ForecastUtilization > maxUsage {
			maxUsage = entry.ForecastUtilization
		}
		switch {
		case maxUsage >= 100:
			entry.Status = "exceeded"
			metric.Exceeded++
		case maxUsage >= 80:
			entry.Status = "warning"
			metric.Warning++
		default:
			entry.Status = "safe"
			metric.Safe++
		}
		metric.FreeTier = append(metric.FreeTier, entry)
	}
	sort.Slice(metric.FreeTier, func(i, j int) bool {
		left := metric.FreeTier[i].ForecastUtilization
		if metric.FreeTier[i].Utilization > left {
			left = metric.FreeTier[i].Utilization
		}
		right := metric.FreeTier[j].ForecastUtilization
		if metric.FreeTier[j].Utilization > right {
			right = metric.FreeTier[j].Utilization
		}
		return left > right
	})
	if len(metric.FreeTier) > 12 {
		metric.FreeTier = metric.FreeTier[:12]
	}
	return metric, nil
}

func (s *OperationalMetricsService) allFreeTierUsage(ctx context.Context) ([]freetierUsage, error) {
	var items []freetierUsage
	var token *string
	for {
		output, err := s.freeTier.GetFreeTierUsage(ctx, &freetier.GetFreeTierUsageInput{MaxResults: aws.Int32(1000), NextToken: token})
		if err != nil {
			return nil, err
		}
		for _, item := range output.FreeTierUsages {
			items = append(items, freetierUsage{Service: item.Service, UsageType: item.UsageType, Unit: item.Unit, ActualUsageAmount: item.ActualUsageAmount, ForecastedUsageAmount: item.ForecastedUsageAmount, Limit: item.Limit})
		}
		if output.NextToken == nil || *output.NextToken == "" {
			break
		}
		token = output.NextToken
	}
	return items, nil
}

type freetierUsage struct {
	Service               *string
	UsageType             *string
	Unit                  *string
	ActualUsageAmount     float64
	ForecastedUsageAmount float64
	Limit                 float64
}
