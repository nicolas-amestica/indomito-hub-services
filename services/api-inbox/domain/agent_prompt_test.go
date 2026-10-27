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

func TestManagedPromptSummarizesProgramsWithoutEmbeddingDocuments(t *testing.T) {
	config := DefaultAgentManagedConfig()
	config.CommercialPolicies = []AgentCommercialPolicy{{ID: "active", Title: "Reserva", Content: "Solicita confirmación antes de reservar.", Active: true, SortOrder: 10}, {ID: "inactive", Title: "Obsoleta", Content: "No debe aparecer.", Active: false}}
	for index := range 78 {
		config.Programs = append(config.Programs, AgentProgram{ID: "program", Name: "Documento comercial que no debe entrar al prompt", Destination: "bariloche", Destinations: []string{"bariloche", "pucon"}, OriginRegions: []string{"v"}, Description: strings.Repeat("detalle ", 40), DocumentKey: "presupuestos/v/bariloche.pdf", Active: true, SortOrder: index})
	}
	prompt := agentSystemPromptWithConfig("56911112222", true, time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC), AgentCollectedData{}, &config)
	if strings.Contains(prompt, "Documento comercial") || strings.Contains(prompt, "detalle detalle") {
		t.Fatal("prompt embeds individual managed programs")
	}
	for _, expected := range []string{"Catalogo administrado: 78 PDF activos", "bariloche, pucon", "Usa buscar_presupuestos"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt is missing compact catalog summary %q", expected)
		}
	}
	if !strings.Contains(prompt, "Reserva: Solicita confirmación antes de reservar.") || strings.Contains(prompt, "Obsoleta") {
		t.Fatal("prompt does not preserve active commercial policies")
	}
	if len(prompt) > 12000 {
		t.Fatalf("managed prompt unexpectedly grew to %d bytes", len(prompt))
	}
}
