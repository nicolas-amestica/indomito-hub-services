package analytics

import (
	"math"
	"testing"
	"time"
)

const testID = "01J00000000000000000000000"

func sampleBudget() Budget {
	return Budget{Name: "Viaje escolar", TotalPassengers: 32, FreePassengers: 2, Days: 5, Nights: 3, PricePerPayer: 100000, TargetMode: "amount", Source: Source{Kind: "manual"}, Taxes: Taxes{VatRate: 19, CrewWithholdingRate: 15.25, Source: "Configuración verificada", CostBasis: "net"}, Categories: []Category{{ID: testID, Name: "Alojamiento"}}, Lines: []Line{{ID: testID, Description: "Hotel", CategoryID: testID, ChargeType: "per_passenger_night", Basis: "total", Quantity: 1, UnitPrice: 100, Currency: "CLP", Rate: 1, RateSource: "CLP", RateDate: "2026-11-18", Days: 3, Nights: 2, Included: true, State: "estimated"}}}
}
func TestBudgetRejectsInvalidFinancialInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Budget)
	}{
		{"sin pagantes", func(b *Budget) { b.FreePassengers = b.TotalPassengers }},
		{"moneda desconocida", func(b *Budget) { b.Lines[0].Currency = "EUR" }},
		{"tasa histórica ausente", func(b *Budget) { b.Lines[0].RateSource = "" }},
		{"fecha inexistente", func(b *Budget) { b.Lines[0].RateDate = "2026-02-30" }},
		{"NaN", func(b *Budget) { b.Lines[0].UnitPrice = math.NaN() }},
		{"margen imposible", func(b *Budget) { b.TargetMode = "margin"; b.Target = 100 }},
		{"escalones sin ordenar", func(b *Budget) { b.Lines[0].Tiers = []Tier{{From: 30, UnitPrice: 100}, {From: 20, UnitPrice: 120}} }},
		{"categoría ajena", func(b *Budget) { b.Lines[0].CategoryID = "01J00000000000000000000001" }},
		{"duplicar relación", func(b *Budget) {
			b.Lines[0].Allocations = []Allocation{{SupplierID: testID, Amount: 10}, {SupplierID: testID, Amount: 20}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := sampleBudget()
			tc.change(&b)
			if b.Validate() == nil {
				t.Fatal("se aceptó entrada inválida")
			}
		})
	}
	if sampleBudget().Validate() != nil {
		t.Fatal("presupuesto válido rechazado")
	}
}
func TestExplicitApprovalAndScenarioIsolation(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	b := sampleBudget()
	s, err := Apply(State{}, Command{CommandID: testID, Action: "SAVE", Budget: &b, Reason: "Presupuesto inicial"}, "actor", now)
	if err != nil || s.Baseline != nil || s.Version != 1 {
		t.Fatalf("guardar no debe aprobar: %+v %v", s, err)
	}
	approved, err := Apply(s, Command{CommandID: testID, Version: 1, Action: "APPROVE", Reason: "Aprobación inicial"}, "approver", now)
	if err != nil || approved.Baseline == nil || approved.Audit.Actor != "approver" {
		t.Fatal(err)
	}
	alternative := sampleBudget()
	alternative.Lines[0].UnitPrice = 200
	scenario, err := Apply(approved, Command{CommandID: testID, Version: 2, Action: "SCENARIO", ScenarioID: testID, Budget: &alternative, Reason: "Hotel alternativo"}, "actor", now)
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Current.Lines[0].UnitPrice != 100 || scenario.Baseline.Lines[0].UnitPrice != 100 {
		t.Fatal("guardar escenario cambió estimación o base")
	}
	promoted, err := Apply(scenario, Command{CommandID: testID, Version: 3, Action: "PROMOTE", ScenarioID: testID, Reason: "Adoptar alternativa"}, "actor", now)
	if err != nil || promoted.Current.Lines[0].UnitPrice != 200 || promoted.Baseline.Lines[0].UnitPrice != 100 {
		t.Fatalf("promoción incorrecta %v", err)
	}
	if approved.Current.Lines[0].UnitPrice != 100 {
		t.Fatal("se mutó snapshot anterior")
	}
	if _, err = Apply(promoted, Command{CommandID: testID, Version: 1, Action: "APPROVE", Reason: "Versión antigua"}, "actor", now); err == nil {
		t.Fatal("se aceptó versión obsoleta")
	}
}
