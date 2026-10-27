package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type agentUsageUpdater interface {
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

// AgentTokenUsage refleja los contadores entregados por Gemini Interactions.
type AgentTokenUsage struct {
	InputTokens   int64 `json:"total_input_tokens" dynamodbav:"inputTokens"`
	OutputTokens  int64 `json:"total_output_tokens" dynamodbav:"outputTokens"`
	ToolUseTokens int64 `json:"total_tool_use_tokens" dynamodbav:"toolUseTokens"`
	ThoughtTokens int64 `json:"total_thought_tokens" dynamodbav:"thoughtTokens"`
	CachedTokens  int64 `json:"total_cached_tokens" dynamodbav:"cachedTokens"`
	TotalTokens   int64 `json:"total_tokens" dynamodbav:"totalTokens"`
}

func (u AgentTokenUsage) empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.ToolUseTokens == 0 && u.ThoughtTokens == 0 && u.CachedTokens == 0 && u.TotalTokens == 0
}

func (s *AgentService) recordTokenUsage(ctx context.Context, usage AgentTokenUsage) error {
	if usage.empty() {
		return nil
	}
	updater, ok := s.db.(agentUsageUpdater)
	if !ok {
		return errors.New("agent usage persistence is unavailable")
	}
	now := s.now().UTC()
	model := strings.TrimSpace(s.gemini.Model)
	_, err := updater.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: "AGENT_USAGE#" + now.Format("2006-01")},
			"sk": &types.AttributeValueMemberS{Value: "MODEL#" + model},
		},
		UpdateExpression: aws.String("ADD calls :one, inputTokens :input, outputTokens :output, toolUseTokens :tool, thoughtTokens :thought, cachedTokens :cached, totalTokens :total SET model = :model, updatedAt = :updated, expiresAt = :expires"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one":     &types.AttributeValueMemberN{Value: "1"},
			":input":   &types.AttributeValueMemberN{Value: fmt.Sprint(usage.InputTokens)},
			":output":  &types.AttributeValueMemberN{Value: fmt.Sprint(usage.OutputTokens)},
			":tool":    &types.AttributeValueMemberN{Value: fmt.Sprint(usage.ToolUseTokens)},
			":thought": &types.AttributeValueMemberN{Value: fmt.Sprint(usage.ThoughtTokens)},
			":cached":  &types.AttributeValueMemberN{Value: fmt.Sprint(usage.CachedTokens)},
			":total":   &types.AttributeValueMemberN{Value: fmt.Sprint(usage.TotalTokens)},
			":model":   &types.AttributeValueMemberS{Value: model},
			":updated": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339)},
			":expires": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(400 * 24 * time.Hour).Unix())},
		},
	})
	if err != nil {
		return fmt.Errorf("persist agent token usage: %w", err)
	}
	return nil
}
