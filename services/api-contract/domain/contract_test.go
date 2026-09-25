package domain

import (
	"testing"
	"time"
)

func TestNewItemRequiresPassengers(t *testing.T) {
	_, err := NewItem("01ARZ3NDEKTSV4RRFFQ69G5FAV", "", nil, "2026-09", Content{}, time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected passenger validation error")
	}
}

func TestNewItemKeepsProgramSnapshot(t *testing.T) {
	reference := &ProgramReference{
		ID:        "program-1",
		Name:      "Brasil 2027",
		UpdatedAt: "2026-09-24T12:00:00Z",
		Content:   map[string]any{"pricing": map[string]any{"usdIncreaseCLP": 100.0}},
	}
	item, err := NewItem(
		"01ARZ3NDEKTSV4RRFFQ69G5FAV",
		reference.ID,
		reference,
		"2027-01",
		Content{Passengers: []Passenger{{Names: "Ana", LastNames: "Perez", DNI: "12345678-5", BirthDate: "2010-01-01", Nationality: "Chilena", Sex: "FEMALE"}}},
		time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if item.ProgramReference == nil || item.ProgramReference.Name != reference.Name {
		t.Fatalf("program reference was not persisted: %#v", item.ProgramReference)
	}
	if got := item.Content.Passengers[0].BirthDate; got != "2010-01-01T00:00:00Z" {
		t.Fatalf("birth date = %q, want UTC ISO", got)
	}
	if got := item.ProgramReference.UpdatedAt; got != "2026-09-24T12:00:00Z" {
		t.Fatalf("program updatedAt = %q, want UTC ISO", got)
	}
}

func TestValidateContentRejectsInvalidPassengerData(t *testing.T) {
	content := Content{Passengers: []Passenger{{Names: "Ana", LastNames: "Perez", DNI: "12345678-9", BirthDate: "2010-02-30", Nationality: "Chilena", Sex: "FEMALE"}}}
	if err := ValidateContent(content); err == nil {
		t.Fatal("expected invalid passenger error")
	}
}

func TestNormalizeDatesConvertsOffsetsToUTC(t *testing.T) {
	content := Content{Trip: Trip{DepartureDate: "2027-01-10T03:00:00-03:00"}}
	if err := NormalizeDates(&content, nil); err != nil {
		t.Fatal(err)
	}
	if got := content.Trip.DepartureDate; got != "2027-01-10T06:00:00Z" {
		t.Fatalf("departure date = %q", got)
	}
}
func TestApprovedAndCancelledAreTerminal(t *testing.T) {
	for _, s := range []Status{StatusApproved, StatusCancelled} {
		if CanTransition(s, StatusDraft) {
			t.Fatalf("%s must be terminal", s)
		}
	}
}
func TestReviewTransitions(t *testing.T) {
	cases := []struct {
		from, to Status
		want     bool
	}{{StatusDraft, StatusPendingApproval, true}, {StatusDraft, StatusApproved, false}, {StatusPendingApproval, StatusApproved, true}, {StatusRejected, StatusDraft, true}}
	for _, tc := range cases {
		if got := CanTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("%s -> %s = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}
