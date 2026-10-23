package domain

import (
	"context"
	"net/http"
	"strings"
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

func TestCreateMeetingDoesNotDuplicateExistingCustomerMeeting(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "oauth2.googleapis.com":
			return jsonResponse(http.StatusOK, `{"access_token":"token"}`), nil
		case "www.googleapis.com":
			if request.Method != http.MethodGet {
				t.Fatalf("unexpected side effect %s %s", request.Method, request.URL)
			}
			return jsonResponse(http.StatusOK, `{"items":[{"id":"existing","status":"confirmed","summary":"AG — Ana","start":{"dateTime":"2026-10-24T10:00:00-03:00"},"end":{"dateTime":"2026-10-24T11:30:00-03:00"}}]}`), nil
		default:
			t.Fatalf("unexpected host %q", request.URL.Host)
			return nil, nil
		}
	})}
	toolbox := &AgentToolbox{meeting: AgentMeetingConfig{CalendarID: "calendar", Timezone: "America/Santiago"}, oauth: GoogleOAuthConfig{ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh"}, http: client, now: func() time.Time { return time.Date(2026, time.October, 23, 12, 0, 0, 0, time.UTC) }}
	result, err := toolbox.createMeeting(context.Background(), "56912345678", map[string]any{"nombreCliente": "Ana", "emailCliente": "ana@example.com", "fechaSugerida": "2026-10-24", "horaSugerida": "10:00"})
	if err != nil {
		t.Fatal(err)
	}
	if boolResult(result, "exito") || !strings.Contains(result["mensaje"].(string), "Ya existe") {
		t.Fatalf("unexpected result %#v", result)
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

func TestAgentDeclarationsOmitEmptyRequiredArrays(t *testing.T) {
	t.Parallel()
	for _, declaration := range agentFunctionDeclarations() {
		parameters, ok := declaration["parameters"].(map[string]any)
		if !ok {
			t.Fatalf("declaration %q has no parameters schema", declaration["name"])
		}
		required, exists := parameters["required"]
		if !exists {
			continue
		}
		values, ok := required.([]string)
		if !ok || len(values) == 0 {
			t.Fatalf("declaration %q contains an invalid required field: %#v", declaration["name"], required)
		}
	}
}

func TestAgentExecutionStateMergesPartialDataAndClearsMeetingOnly(t *testing.T) {
	t.Parallel()
	state := &agentExecutionState{}
	state.mergeQuote(map[string]any{"destino": "Bariloche", "codigoRegion": "V", "ciudad": "Viña del Mar"})
	state.mergeMeeting(map[string]any{"nombreCliente": "Ana", "emailCliente": "ANA@EXAMPLE.COM"})
	state.mergeMeeting(map[string]any{"fechaSugerida": "2026-10-24", "horaSugerida": "10:00"})
	if !state.collectedDirty || state.collected.Destination != "bariloche" || state.collected.Region != "v" || state.collected.City != "vina_del_mar" {
		t.Fatalf("unexpected quote state %#v", state.collected)
	}
	if state.collected.MeetingName != "Ana" || state.collected.MeetingMail != "ana@example.com" || state.collected.MeetingDate != "2026-10-24" || state.collected.MeetingTime != "10:00" {
		t.Fatalf("unexpected meeting state %#v", state.collected)
	}
	state.collectedDirty = false
	state.clearMeeting()
	if !state.collectedDirty || state.collected.MeetingName != "" || state.collected.MeetingMail != "" || state.collected.Destination != "bariloche" {
		t.Fatalf("meeting clear damaged collected state %#v", state.collected)
	}
}

func TestAgentSystemPromptIncludesPersistedStructuredData(t *testing.T) {
	t.Parallel()
	prompt := agentSystemPrompt("56912345678", true, time.Date(2026, time.October, 23, 12, 0, 0, 0, time.UTC), AgentCollectedData{MeetingName: "Ana", MeetingMail: "ana@example.com"})
	for _, expected := range []string{`"nombreCliente":"Ana"`, `"emailCliente":"ana@example.com"`, "datos persistidos"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt does not include %q", expected)
		}
	}
}
