package functions

import (
	"os"
	"strings"
	"testing"
)

func TestServerlessDefinesEncryptedQueuesDlqsAndAlarms(t *testing.T) {
	raw, err := os.ReadFile("../serverless.ts")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, fragment := range []string{
		"InboxWorkflowQueue", "InboxWorkflowDeadLetterQueue",
		"InboxRealtimeQueue", "InboxRealtimeDeadLetterQueue",
		"maxReceiveCount: 5", "MessageRetentionPeriod: 1209600",
		"SqsManagedSseEnabled: true", "AWS::CloudWatch::Alarm",
		"fn-despachar-trabajo-inbox-v1", "ConversacionesTableStreamArn",
		"ReportBatchItemFailures", "sqs:SendMessage",
		"fn-procesar-mensaje-entrante-v1", "InboxInboundQueue", "InboxInboundDeadLetterQueue",
		"fn-procesar-agente-inbox-v1", "InboxAgentQueue", "InboxAgentDeadLetterQueue",
		"fn-conectar-websocket-v1", "fn-desconectar-websocket-v1", "$connect", "$disconnect",
		"fn-publicar-evento-inbox-v1", "execute-api:ManageConnections", "WebSocketManagementEndpoint",
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("serverless infrastructure is missing %q", fragment)
		}
	}
}
