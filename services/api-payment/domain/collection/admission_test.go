package collection

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func admissionFixture() (Admission, Audit) {
	return Admission{AccountID: "new-account", ParticipantID: "new-person", TripID: "trip", AnnexID: "annex", DepositAgreed: 10000, Installments: []AgreedInstallment{{ID: "01", DueDate: "2027-01-31", Amount: 20000}, {ID: "02", DueDate: "2027-02-28", Amount: 20001}}}, Audit{CommandID: "admit", Actor: "operator", Reason: "approved annex", RecordedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
}

func TestAdmissionCreatesOnlyAgreedDebt(t *testing.T) {
	input, audit := admissionFixture()
	change, err := Admit(input, audit)
	if err != nil {
		t.Fatal(err)
	}
	want := Account{ID: input.AccountID, ParticipantID: input.ParticipantID, TripID: input.TripID, Version: 1, Active: true, DepositAgreed: 10000, Installments: []Installment{{ID: "01", DueDate: "2027-01-31", Original: 20000}, {ID: "02", DueDate: "2027-02-28", Original: 20001}}}
	if !reflect.DeepEqual(change.Account, want) || change.Event.Amount != 50001 || change.Event.Reference != "annex" || len(change.Event.Entries) != 0 {
		t.Fatalf("unexpected admission: %+v", change)
	}
	input.Installments[0].Amount = 1
	if change.Account.Installments[0].Original != 20000 {
		t.Fatal("admission aliases caller state")
	}
}

func TestAdmissionRejectsInvalidTerms(t *testing.T) {
	tests := map[string]func(*Admission, *Audit){
		"missing annex":       func(a *Admission, _ *Audit) { a.AnnexID = "" },
		"missing actor":       func(_ *Admission, a *Audit) { a.Actor = "" },
		"missing identity":    func(a *Admission, _ *Audit) { a.ParticipantID = "" },
		"free with debt":      func(a *Admission, _ *Audit) { a.Free = true },
		"negative deposit":    func(a *Admission, _ *Audit) { a.DepositAgreed = -1 },
		"zero quota":          func(a *Admission, _ *Audit) { a.Installments[0].Amount = 0 },
		"overflow":            func(a *Admission, _ *Audit) { a.DepositAgreed = MaxAmount },
		"invalid date":        func(a *Admission, _ *Audit) { a.Installments[1].DueDate = "2027-02-30" },
		"duplicate date":      func(a *Admission, _ *Audit) { a.Installments[1].DueDate = a.Installments[0].DueDate },
		"duplicate id":        func(a *Admission, _ *Audit) { a.Installments[1].ID = a.Installments[0].ID },
		"payer without price": func(a *Admission, _ *Audit) { a.DepositAgreed = 0; a.Installments = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input, audit := admissionFixture()
			mutate(&input, &audit)
			if _, err := Admit(input, audit); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid terms accepted: %v", err)
			}
		})
	}
}

func TestAdmissionSupportsIdentifiedFreeParticipant(t *testing.T) {
	input, audit := admissionFixture()
	input.Free, input.DepositAgreed, input.Installments = true, 0, nil
	change, err := Admit(input, audit)
	if err != nil || !change.Account.Free || change.Event.Amount != 0 || len(change.Account.Installments) != 0 || len(change.Event.Entries) != 0 {
		t.Fatalf("free participant: %+v %v", change, err)
	}
}
