package domain

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const metaGraphMetricNamespace = "Indomito/Inbox"

func recordMetaGraphCall(operation, outcome string, startedAt time.Time) {
	stage := os.Getenv("APP_STAGE")
	if stage == "" {
		stage = "unknown"
	}
	payload, err := buildMetaGraphMetric(stage, operation, outcome, startedAt, time.Now())
	if err != nil {
		return
	}
	fmt.Println(string(payload))
}

func buildMetaGraphMetric(stage, operation, outcome string, startedAt, finishedAt time.Time) ([]byte, error) {
	metrics := []map[string]string{
		{"Name": "GraphApiRequests", "Unit": "Count"},
		{"Name": "GraphApiLatency", "Unit": "Milliseconds"},
	}
	if outcome != "success" {
		metrics = append(metrics, map[string]string{"Name": "GraphApiFailures", "Unit": "Count"})
	}
	payload := map[string]any{
		"_aws": map[string]any{
			"Timestamp": finishedAt.UnixMilli(),
			"CloudWatchMetrics": []any{map[string]any{
				"Namespace":  metaGraphMetricNamespace,
				"Dimensions": [][]string{{"Stage"}, {"Stage", "Operation", "Outcome"}},
				"Metrics":    metrics,
			}},
		},
		"Stage":            stage,
		"Operation":        operation,
		"Outcome":          outcome,
		"GraphApiRequests": 1,
		"GraphApiLatency":  max(0, finishedAt.Sub(startedAt).Milliseconds()),
	}
	if outcome != "success" {
		payload["GraphApiFailures"] = 1
	}
	return json.Marshal(payload)
}

func metaGraphStatusOutcome(statusCode int) string {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return "success"
	case statusCode >= 400 && statusCode < 500:
		return "http_4xx"
	case statusCode >= 500:
		return "http_5xx"
	default:
		return "http_other"
	}
}
