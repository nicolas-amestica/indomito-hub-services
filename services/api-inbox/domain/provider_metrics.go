package domain

import (
	"encoding/json"
	"log"
	"os"
	"time"
)

const providerMetricNamespace = "Indomito/Inbox"

func emitProviderFailure(metricName, operation string) {
	payload, err := buildProviderFailureMetric(metricName, operation, os.Getenv("APP_STAGE"), time.Now().UTC())
	if err == nil {
		log.Print(string(payload))
	}
}

func buildProviderFailureMetric(metricName, operation, stage string, occurredAt time.Time) ([]byte, error) {
	return json.Marshal(map[string]any{
		"_aws": map[string]any{
			"Timestamp": occurredAt.UnixMilli(),
			"CloudWatchMetrics": []any{map[string]any{
				"Namespace":  providerMetricNamespace,
				"Dimensions": [][]string{{"Stage"}},
				"Metrics":    []any{map[string]string{"Name": metricName, "Unit": "Count"}},
			}},
		},
		"Stage": stage, "Operation": operation, metricName: 1,
	})
}
