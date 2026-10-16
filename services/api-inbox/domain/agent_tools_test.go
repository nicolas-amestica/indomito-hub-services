package domain

import (
	"testing"
	"time"
)

func TestFileContainsDestinationUsesWholeSegments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, file, destination string
		want                    bool
	}{
		{name: "single destination", file: "bariloche-1.pdf", destination: "bariloche", want: true},
		{name: "multiple destinations", file: "pucon-bariloche-valdivia-2.pdf", destination: "bariloche", want: true},
		{name: "correlative is not destination", file: "bariloche-1.pdf", destination: "1", want: false},
		{name: "partial segment is rejected", file: "puerto_varas-1.pdf", destination: "puerto", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := fileContainsDestination(test.file, test.destination); got != test.want {
				t.Fatalf("fileContainsDestination() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBudgetRegionByCityMatchesLegacyFallbacks(t *testing.T) {
	t.Parallel()
	for city, want := range map[string]string{"arica": "xv", "san_pedro_de_atacama": "ii", "vina_del_mar": "v", "santiago": "rm", "chillan": "xvi", "puerto_varas": "x", "punta_arenas": "xii"} {
		if got := budgetRegionByCity[city]; got != want {
			t.Errorf("budgetRegionByCity[%q] = %q, want %q", city, got, want)
		}
	}
}

func TestAvailableSlotsHonorsScheduleAdvanceAndOverlap(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	toolbox := &AgentToolbox{meeting: AgentMeetingConfig{Timezone: "America/Santiago", SlotDurationMinutes: 90, MinAdvanceMinutes: 120, WeekdayStartHour: 10, WeekdayEndHour: 22, SaturdayStartHour: 10, SaturdayEndHour: 14}, now: func() time.Time { return time.Date(2026, time.October, 19, 7, 0, 0, 0, location) }}
	event := calendarEvent{ID: "busy"}
	event.Start.DateTime = "2026-10-19T11:30:00-03:00"
	event.End.DateTime = "2026-10-19T13:00:00-03:00"
	slots := toolbox.availableSlots(time.Date(2026, time.October, 19, 0, 0, 0, 0, location), []calendarEvent{event}, "")
	if contains(slots, "11:30") {
		t.Fatal("occupied slot was returned")
	}
	if !contains(slots, "10:00") || !contains(slots, "13:00") {
		t.Fatalf("expected neighboring slots, got %v", slots)
	}
}

func TestChileanHolidayAndSundayHaveNoSlots(t *testing.T) {
	t.Parallel()
	toolbox := &AgentToolbox{meeting: AgentMeetingConfig{SlotDurationMinutes: 90, WeekdayStartHour: 10, WeekdayEndHour: 22}, now: func() time.Time { return time.Time{} }}
	if !isChileanHoliday(time.Date(2026, time.September, 18, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("September 18 must be treated as a holiday")
	}
	if slots := toolbox.availableSlots(time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC), nil, ""); len(slots) != 0 {
		t.Fatalf("Sunday returned slots: %v", slots)
	}
}

func TestAgentDeclaresEveryLegacyCommercialTool(t *testing.T) {
	t.Parallel()
	declared := map[string]bool{}
	for _, declaration := range agentFunctionDeclarations() {
		name, _ := declaration["name"].(string)
		declared[name] = true
	}
	for _, required := range []string{"buscar_presupuestos", "listar_reuniones", "guardar_datos_reunion", "crear_reunion", "listar_reunion", "editar_reunion", "cancelar_reunion", "enviar_certificado_sernatur", "sin_respuesta"} {
		if !declared[required] {
			t.Fatalf("missing tool declaration %q", required)
		}
	}
}
