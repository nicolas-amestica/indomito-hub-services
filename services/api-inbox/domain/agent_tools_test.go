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
	if boolResult(result, "exito") || stringArg(result, "codigo") != "MEETING_ALREADY_EXISTS" || !strings.Contains(result["mensaje"].(string), "Ya existe") {
		t.Fatalf("unexpected result %#v", result)
	}
}

func TestMeetingRejectsInvalidCustomerEmailBeforeCalendar(t *testing.T) {
	t.Parallel()
	for _, email := range []string{"correo@sfsdfsdaf.aksjdahgsd", "sin-arroba", "Nombre <correo@gmail.com>", "correo@localhost"} {
		if validCustomerEmail(email) {
			t.Fatalf("invalid email was accepted: %q", email)
		}
	}
	for _, email := range []string{"niamesvi3@gmail.com", "info@girasindomito.cl"} {
		if !validCustomerEmail(email) {
			t.Fatalf("valid email was rejected: %q", email)
		}
	}
	toolbox := &AgentToolbox{}
	result, err := toolbox.createMeeting(context.Background(), "56912345678", map[string]any{
		"nombreCliente": "Nicolás", "emailCliente": "correo@sfsdfsdaf.aksjdahgsd", "fechaSugerida": "2026-10-30", "horaSugerida": "13:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if boolResult(result, "exito") || stringArg(result, "codigo") != "MEETING_INVALID_CONTACT" {
		t.Fatalf("invalid email reached calendar flow: %#v", result)
	}
}

func TestMeetingMutationsReturnNotFoundWithoutCreatingReplacement(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "oauth2.googleapis.com":
			return jsonResponse(http.StatusOK, `{"access_token":"token"}`), nil
		case "www.googleapis.com":
			if request.Method != http.MethodGet {
				t.Fatalf("unexpected calendar mutation %s", request.Method)
			}
			return jsonResponse(http.StatusOK, `{"items":[]}`), nil
		default:
			t.Fatalf("unexpected host %q", request.URL.Host)
			return nil, nil
		}
	})}
	toolbox := &AgentToolbox{meeting: AgentMeetingConfig{CalendarID: "calendar", Timezone: "America/Santiago"}, oauth: GoogleOAuthConfig{ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh"}, http: client, now: time.Now}
	updated, err := toolbox.updateMeeting(context.Background(), "56912345678", map[string]any{"nuevaFecha": "2026-10-30", "nuevaHora": "13:00", "emailCliente": "niamesvi3@gmail.com"})
	if err != nil || stringArg(updated, "codigo") != "MEETING_NOT_FOUND" {
		t.Fatalf("missing update result=%#v err=%v", updated, err)
	}
	cancelled, err := toolbox.cancelMeeting(context.Background(), "56912345678")
	if err != nil || stringArg(cancelled, "codigo") != "MEETING_NOT_FOUND" {
		t.Fatalf("missing cancellation result=%#v err=%v", cancelled, err)
	}
}

func TestVerifyCreatedMeetingRequiresPersistedCalendarEvidence(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "oauth2.googleapis.com":
			return jsonResponse(http.StatusOK, `{"access_token":"token"}`), nil
		case "www.googleapis.com":
			if request.Method != http.MethodGet || !strings.HasSuffix(request.URL.Path, "/events/event-1") {
				t.Fatalf("unexpected calendar verification request %s %s", request.Method, request.URL)
			}
			return jsonResponse(http.StatusOK, `{"id":"event-1","status":"confirmed","hangoutLink":"https://meet.google.com/new-link","start":{"dateTime":"2026-10-29T10:00:00-03:00"},"attendees":[{"email":"niamesvi3@gmail.com"}]}`), nil
		default:
			t.Fatalf("unexpected host %q", request.URL.Host)
			return nil, nil
		}
	})}
	toolbox := &AgentToolbox{
		meeting: AgentMeetingConfig{CalendarID: "calendar"},
		oauth:   GoogleOAuthConfig{ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh"},
		http:    client,
	}
	if err := toolbox.verifyCreatedMeeting(context.Background(), calendarEvent{ID: "event-1"}, "niamesvi3@gmail.com", "2026-10-29", "10:00"); err != nil {
		t.Fatal(err)
	}
	if err := toolbox.verifyCreatedMeeting(context.Background(), calendarEvent{ID: "event-1"}, "otro@example.com", "2026-10-29", "10:00"); err == nil {
		t.Fatal("verification accepted an event without the requested attendee")
	}
}

func TestCreateMeetingAcceptsContactExplicitInLatestMessage(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "oauth2.googleapis.com":
			return jsonResponse(http.StatusOK, `{"access_token":"token"}`), nil
		case "www.googleapis.com":
			if request.Method == http.MethodPost {
				return jsonResponse(http.StatusOK, `{"id":"event-1","status":"confirmed","hangoutLink":"https://meet.google.com/new-link"}`), nil
			}
			if strings.HasSuffix(request.URL.Path, "/events/event-1") {
				return jsonResponse(http.StatusOK, `{"id":"event-1","status":"confirmed","hangoutLink":"https://meet.google.com/new-link","start":{"dateTime":"2026-10-29T14:30:00-03:00"},"attendees":[{"email":"niamesvi3@gmail.com"}]}`), nil
			}
			return jsonResponse(http.StatusOK, `{"items":[]}`), nil
		default:
			t.Fatalf("unexpected host %q", request.URL.Host)
			return nil, nil
		}
	})}
	now := time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)
	toolbox := &AgentToolbox{
		meeting: AgentMeetingConfig{CalendarID: "calendar", Timezone: "America/Santiago", SlotDurationMinutes: 90, MinAdvanceMinutes: 120, WeekdayStartHour: 10, WeekdayEndHour: 22, SaturdayStartHour: 10, SaturdayEndHour: 14},
		oauth:   GoogleOAuthConfig{ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh"},
		http:    client,
		now:     func() time.Time { return now },
	}
	state := &agentExecutionState{latestCustomerMessage: "Nicolas niamesvi3@gmail.com", currentCustomerTurn: "Nicolas niamesvi3@gmail.com"}
	result, _, err := toolbox.execute(context.Background(), "56959139687", "message-1", "crear_reunion", map[string]any{
		"nombreCliente": "Nicolás", "emailCliente": "niamesvi3@gmail.com", "fechaSugerida": "2026-10-29", "horaSugerida": "14:30",
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if !boolResult(result, "exito") || !state.meetingCreated || stringArg(result, "meetLink") != "https://meet.google.com/new-link" {
		t.Fatalf("explicit contact did not create a verified meeting: result=%#v state=%#v", result, state)
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

func TestQuoteOriginMustComeFromCustomerEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 29, 3, 23, 0, 0, time.UTC)
	tests := []struct {
		name       string
		message    string
		args       map[string]any
		collected  AgentCollectedData
		wantOK     bool
		wantRegion string
		wantCity   string
	}{
		{name: "rejects invented region", message: "Información de viajes a Bariloche para febrero", args: map[string]any{"destino": "bariloche", "codigoRegion": "viii"}},
		{name: "accepts explicit roman region", message: "Salimos desde la Región VIII", args: map[string]any{"destino": "bariloche", "codigoRegion": "v"}, wantOK: true, wantRegion: "viii"},
		{name: "maps explicit city", message: "El grupo sale desde Concepción", args: map[string]any{"destino": "bariloche", "codigoRegion": "v"}, wantOK: true, wantRegion: "viii", wantCity: "concepcion"},
		{name: "accepts previously confirmed origin", message: "Envíame los programas", args: map[string]any{"destino": "bariloche", "codigoRegion": "i"}, collected: AgentCollectedData{Region: "vi", City: "rancagua", QuoteOriginConfirmedAt: now.Add(-time.Hour)}, wantOK: true, wantRegion: "vi", wantCity: "rancagua"},
		{name: "rejects legacy unconfirmed state", message: "Envíame los programas", args: map[string]any{"destino": "bariloche", "codigoRegion": "viii"}, collected: AgentCollectedData{Region: "viii"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := &agentExecutionState{currentCustomerTurn: test.message, collected: test.collected}
			got := state.confirmQuoteOrigin(test.args, now)
			if got != test.wantOK {
				t.Fatalf("confirmQuoteOrigin() = %v, want %v", got, test.wantOK)
			}
			if got && (stringArg(test.args, "codigoRegion") != test.wantRegion || stringArg(test.args, "ciudad") != test.wantCity) {
				t.Fatalf("resolved origin = %q/%q, want %q/%q", stringArg(test.args, "codigoRegion"), stringArg(test.args, "ciudad"), test.wantRegion, test.wantCity)
			}
		})
	}
}

func TestQuoteDestinationAmbiguityRequiresClarification(t *testing.T) {
	t.Parallel()
	if !hasAmbiguousQuoteDestination("Información de viajes a Bariloche o Sur de Chile") {
		t.Fatal("alternative destinations were not detected")
	}
	if hasAmbiguousQuoteDestination("Quiero viajar a Bariloche desde la Región VIII") {
		t.Fatal("single destination was marked ambiguous")
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
	now := time.Date(2026, time.October, 23, 12, 0, 0, 0, time.UTC)
	state.mergeQuote(map[string]any{"destino": "Bariloche", "codigoRegion": "V", "ciudad": "Viña del Mar"}, time.Now().UTC())
	state.mergeMeeting(map[string]any{"nombreCliente": "Ana", "emailCliente": "ANA@EXAMPLE.COM"}, "Soy Ana y mi correo es ana@example.com", now)
	state.mergeMeeting(map[string]any{"fechaSugerida": "2026-10-24", "horaSugerida": "10:00"}, "El 24 a las 10", now)
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

func TestMeetingContactMustComeFromCurrentCustomerFlow(t *testing.T) {
	now := time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)
	state := &agentExecutionState{}
	state.mergeMeeting(map[string]any{"fechaSugerida": "2026-10-30"}, "El 30 de octubre", now)
	if state.collected.MeetingDate != "2026-10-30" || state.collected.MeetingUpdatedAt != now || !state.collected.MeetingContactConfirmedAt.IsZero() {
		t.Fatalf("partial non-contact meeting data was not preserved safely: %#v", state.collected)
	}
	state.mergeMeeting(map[string]any{"nombreCliente": "Contacto antiguo", "emailCliente": "contacto@girasindomito.cl"}, "Quiero reagendar", now)
	if state.collected.MeetingName != "" || state.collected.MeetingMail != "" || state.hasConfirmedMeetingContact(map[string]any{"nombreCliente": "Contacto antiguo", "emailCliente": "contacto@girasindomito.cl"}, now) {
		t.Fatalf("stale inferred contact was accepted: %#v", state.collected)
	}
	state.mergeMeeting(map[string]any{"nombreCliente": "Nicolás", "emailCliente": "niamesvi3@gmail.com"}, "Nicolas niamesvi3@gmail.com", now)
	if !state.hasConfirmedMeetingContact(map[string]any{"nombreCliente": "Nicolás", "emailCliente": "niamesvi3@gmail.com"}, now.Add(time.Hour)) {
		t.Fatal("explicit current contact was not accepted")
	}
	if state.hasConfirmedMeetingContact(map[string]any{"nombreCliente": "Nicolás", "emailCliente": "niamesvi3@gmail.com"}, now.Add(3*time.Hour)) {
		t.Fatal("expired meeting contact remained valid")
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

func TestHandoffToolRejectsStaleHumanRequest(t *testing.T) {
	toolbox := &AgentToolbox{}
	state := &agentExecutionState{latestCustomerMessage: "Hola"}
	result, silent, err := toolbox.execute(context.Background(), "56911112222", "wamid-1", "sin_respuesta", map[string]any{"motivo": "Cliente solicita nuevamente atención humana."}, state)
	if err != nil || silent || state.derivation != nil || result["derivado"] != false {
		t.Fatalf("expected stale handoff to be rejected: result=%v silent=%v derivation=%v err=%v", result, silent, state.derivation, err)
	}
	state.latestCustomerMessage = "Quiero hablar con una persona"
	result, silent, err = toolbox.execute(context.Background(), "56911112222", "wamid-2", "sin_respuesta", map[string]any{"motivo": "Cliente solicita atención humana."}, state)
	if err != nil || !silent || state.derivation == nil || result["derivado"] != true {
		t.Fatalf("expected current handoff to be accepted: result=%v silent=%v derivation=%v err=%v", result, silent, state.derivation, err)
	}
}
