package domain

import (
	"reflect"
	"testing"
)

func TestBuildManagedProgramsPreservesLegacyRoutes(t *testing.T) {
	t.Parallel()
	keys := []string{
		"presupuestos/v/bariloche-1.pdf",
		"presupuestos/v/bariloche-2.pdf",
		"presupuestos/v/pucon-puerto_varas-chiloe-valdivia-sur_de_chile-1.pdf",
		"presupuestos/excepciones/salamanca/bariloche-1.pdf",
	}
	programs, err := BuildManagedProgramsFromLegacyKeys(keys, "presupuestos/", "presupuestos/excepciones/")
	if err != nil {
		t.Fatal(err)
	}
	if len(programs) != len(keys) {
		t.Fatalf("got %d programs, want %d", len(programs), len(keys))
	}
	for _, program := range programs {
		if len(program.Destinations) == 0 || program.Destination != program.Destinations[0] {
			t.Fatalf("program does not preserve the legacy primary destination: %#v", program)
		}
	}
	if got := managedBudgetKeys(programs, "pucon", "v", ""); !reflect.DeepEqual(got, []string{keys[2]}) {
		t.Fatalf("unexpected multidestination result %v", got)
	}
	if got := managedBudgetKeys(programs, "bariloche", "iv", "salamanca"); !reflect.DeepEqual(got, []string{keys[3]}) {
		t.Fatalf("city exception must replace regional results, got %v", got)
	}
}

func TestManagedBudgetKeysSupportsLegacyDestinationAndDeduplicates(t *testing.T) {
	t.Parallel()
	programs := []AgentProgram{
		{ID: "old", Destination: "bariloche", OriginRegions: []string{"v"}, DocumentKey: "presupuestos/v/bariloche-1.pdf", Active: true},
		{ID: "new", Destinations: []string{"bariloche", "pucon"}, OriginRegions: []string{"v"}, DocumentKey: "presupuestos/v/bariloche-1.pdf", Active: true},
	}
	if got := managedBudgetKeys(programs, "bariloche", "v", ""); !reflect.DeepEqual(got, []string{"presupuestos/v/bariloche-1.pdf"}) {
		t.Fatalf("unexpected deduplicated result %v", got)
	}
}

func TestBuildManagedProgramsRejectsUnexpectedKey(t *testing.T) {
	t.Parallel()
	if _, err := BuildManagedProgramsFromLegacyKeys([]string{"presupuestos/v/bariloche-final.pdf"}, "presupuestos/", "presupuestos/excepciones/"); err == nil {
		t.Fatal("expected invalid correlative error")
	}
}
