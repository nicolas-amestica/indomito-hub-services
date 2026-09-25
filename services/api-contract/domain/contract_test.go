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
		validContent(),
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

func validContent() Content {
	return Content{
		Representatives:       []Person{{Name: "Susana Guevara", DNI: "16915292-6"}},
		Institution:           Institution{Name: "Colegio", Address: "Direccion 123", Course: "4 medio"},
		ClientRepresentatives: []Person{{Name: "Ana Perez", DNI: "12345678-5", Course: "4 medio"}},
		Trip:                  Trip{City: "Santiago", ContractDate: "2026-09-24", Destination: "Brasil", DepartureDate: "2027-01-10", ReturnDate: "2027-01-14", Days: 5, Nights: 4, DeparturePoint: "Colegio"},
		Plan:                  Plan{Name: "Brasil 2027", ServicesIncluded: []Service{{Description: "Transporte"}}},
		Payments: Payments{
			FreePassengers: 1, PricePerPerson: 500000, DownPayment: 100000, DaysBeforePayment: 10, MaxExchangeRate: 1100,
			Installments: Installments{Quantity: 5, StartMonth: "Enero"},
			Conditions:   Conditions{SpecialProgramDeposit: 100000},
			BankAccount:  BankAccount{AccountNumber: "90278249343", AccountHolder: "Giras Indomito Ltda.", HolderDNI: "77.654.796-4", Bank: "Banco Estado · Cuenta Vista", Email: "pagos@work.girasindomito.cl"},
		},
		Passengers: []Passenger{{Names: "Ana", LastNames: "Perez", DNI: "12345678-5", BirthDate: "2010-01-01", Nationality: "Chile", Sex: "FEMALE"}},
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
