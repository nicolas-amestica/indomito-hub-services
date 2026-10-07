package domain

import (
	"reflect"
	"testing"
)

func TestMonthlyDueDatesKeepAnchorAcrossShortMonths(t *testing.T) {
	for _, tc := range []struct {
		name string
		i    Installments
		want []string
	}{
		{"normal February", Installments{StartYear: 2027, StartMonth: "01", StartDay: 31, Quantity: 4}, []string{"2027-01-31", "2027-02-28", "2027-03-31", "2027-04-30"}},
		{"leap February", Installments{StartYear: 2028, StartMonth: "01", StartDay: 31, Quantity: 3}, []string{"2028-01-31", "2028-02-29", "2028-03-31"}},
		{"year rollover", Installments{StartYear: 2027, StartMonth: "12", StartDay: 30, Quantity: 3}, []string{"2027-12-30", "2028-01-30", "2028-02-29"}},
		{"historical month name", Installments{StartYear: 2028, StartMonth: "Febrero", StartDay: 29, Quantity: 2}, []string{"2028-02-29", "2028-03-29"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.i.DueDates()
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v err %v", got, err)
			}
		})
	}
}

func TestMonthlyDueDatesRejectMissingAndImpossibleInputs(t *testing.T) {
	for _, i := range []Installments{
		{StartYear: 2027, StartMonth: "02", StartDay: 29, Quantity: 2},
		{StartYear: 2100, StartMonth: "02", StartDay: 29, Quantity: 2},
		{StartYear: 2027, StartMonth: "04", StartDay: 31, Quantity: 2},
		{StartMonth: "01", StartDay: 1, Quantity: 2},
		{StartYear: 2027, StartMonth: "01", Quantity: 2},
		{StartYear: 2027, StartMonth: "13", StartDay: 1, Quantity: 2},
		{StartYear: 2027, StartMonth: "01", StartDay: 1, Quantity: 121},
	} {
		if _, err := i.DueDates(); err == nil {
			t.Fatalf("accepted %+v", i)
		}
	}
}

func TestCurrentTermsReplaceFixedPenaltyWithoutMutatingLegacyContent(t *testing.T) {
	legacy := validContent()
	legacy.Payments.Conditions.CancellationPenaltyPercentage = 25
	current := legacy
	UseCurrentTerms(&current)
	if legacy.Payments.Conditions.CancellationPenaltyPercentage != 25 || legacy.Payments.Conditions.RefundPolicyVersion != 0 {
		t.Fatal("historical policy mutated")
	}
	if current.Payments.Conditions.RefundPolicyVersion != 2 || current.Payments.Conditions.CancellationPenaltyPercentage != 0 {
		t.Fatal("new policy not applied")
	}
	current.Payments.Installments.StartYear = 0
	if err := ValidateContent(current); err == nil {
		t.Fatal("current contract accepted unknown installment year")
	}
}
