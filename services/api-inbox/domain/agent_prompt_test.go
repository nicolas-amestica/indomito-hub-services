package domain

import (
	"strings"
	"testing"
	"time"
)

func TestAgentPromptUsesAIPersonaAndCurrentCommercialRules(t *testing.T) {
	prompt := agentSystemPrompt("56911112222", false, time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), AgentCollectedData{})
	for _, expected := range []string{"Agente IA de Giras Indómito", "No existe descuento por pago en efectivo", "cantidad o combinación de actividades", "pide explícitamente hablar con una persona"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt is missing %q", expected)
		}
	}
	if strings.Contains(prompt, "Una cuota pagada en efectivo tiene") {
		t.Fatal("prompt contains obsolete cash discount")
	}
}
