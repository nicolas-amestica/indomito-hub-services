package domain

import (
	"testing"
)

func TestOptionalTravelDates(t *testing.T) {
	for _, tc := range []struct {
		name, departure, arrival string
		valid                    bool
	}{
		{"undefined", "", "", true},
		{"complete", "2027-01-10", "2027-01-14", true},
		{"missing return", "2027-01-10", "", false},
		{"missing departure", "", "2027-01-14", false},
		{"reversed", "2027-01-14", "2027-01-10", false},
		{"wrong duration", "2027-01-10", "2027-01-11", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := validContent()
			content.Trip.DepartureDate, content.Trip.ReturnDate = tc.departure, tc.arrival
			err := NormalizeDates(&content, nil)
			if err == nil {
				err = ValidateContent(content)
			}
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestUndefinedTravelStillRequiresSigningDateAndDuration(t *testing.T) {
	for _, field := range []string{"signing date", "duration"} {
		t.Run(field, func(t *testing.T) {
			content := validContent()
			content.Trip.DepartureDate, content.Trip.ReturnDate = "", ""
			if field == "signing date" {
				content.Trip.ContractDate = ""
			} else {
				content.Trip.Days = 0
			}
			if err := ValidateContent(content); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}
