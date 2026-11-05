package collection

import (
	"testing"
	"time"

	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
)

func TestMinimumSIISubmissionQueryDelay(t *testing.T) {
	t.Parallel()
	if minimumSIISubmissionQueryDelay != 2*time.Minute {
		t.Fatalf("minimumSIISubmissionQueryDelay = %s, want 2m", minimumSIISubmissionQueryDelay)
	}
}

func TestFinalSubmissionStatusWaitsForSIIStatistics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status sii.SubmissionStatus
		want   bool
	}{
		{name: "processed without statistics", status: sii.SubmissionStatus{State: "EPR"}, want: false},
		{name: "processed with statistics", status: sii.SubmissionStatus{State: "EPR", Informed: 1, Accepted: 1}, want: true},
		{name: "schema rejected", status: sii.SubmissionStatus{State: "RSC"}, want: true},
		{name: "still processing", status: sii.SubmissionStatus{State: "-11"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := finalSubmissionStatus(test.status); got != test.want {
				t.Fatalf("finalSubmissionStatus() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestFinalSubmissionStatusRecognizesOfficialRESTTerminalStates(t *testing.T) {
	for _, state := range []string{"RCH", "RCO", "VOF", "RFR", "RPT", "RSC", "RCT"} {
		if !finalSubmissionStatus(sii.SubmissionStatus{State: state}) {
			t.Fatalf("state %s must be terminal", state)
		}
	}
	for _, state := range []string{"REC", "CRT", "FOK", "PRD", "SOK"} {
		if finalSubmissionStatus(sii.SubmissionStatus{State: state}) {
			t.Fatalf("state %s must remain pending", state)
		}
	}
	if !finalSubmissionStatus(sii.SubmissionStatus{State: "RPR", Informed: 1, Repairs: 1}) {
		t.Fatal("RPR with statistics must be terminal")
	}
}

func TestValidSIINotificationEmail(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "", want: true},
		{value: "sii-dev@work.girasindomito.cl", want: true},
		{value: "Nombre <sii@work.girasindomito.cl>", want: false},
		{value: "correo-invalido", want: false},
	} {
		if got := validSIINotificationEmail(test.value); got != test.want {
			t.Fatalf("validSIINotificationEmail(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}
