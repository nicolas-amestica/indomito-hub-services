package collection

import (
	"context"
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

func TestTaxSubmissionStatusUpdatesPublicProjection(t *testing.T) {
	tests := []struct {
		name       string
		status     sii.SubmissionStatus
		wantStatus string
		wantPublic string
	}{
		{name: "accepted", status: sii.SubmissionStatus{State: "EPR", Informed: 1, Accepted: 1}, wantStatus: taxStatusAccepted, wantPublic: "ACCEPTED"},
		{name: "accepted with repairs", status: sii.SubmissionStatus{State: "RPR", Informed: 1, Repairs: 1}, wantStatus: taxStatusAcceptedRepair, wantPublic: "ACCEPTED_WITH_REPAIRS"},
		{name: "rejected", status: sii.SubmissionStatus{State: "RCH", Informed: 1, Rejected: 1}, wantStatus: taxStatusRejected, wantPublic: "REJECTED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, db := serviceFixture(t)
			now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)
			submittedAt := now.Add(-5 * time.Minute)
			request := TaxDocumentRequest{RequestID: approvedID, Status: taxStatusSubmitted, SIIIssuance: &SIIIssuance{Folio: 381, TrackID: "track", SubmittedAt: &submittedAt}, DeliveryStatus: "SMTP_ACCEPTED"}
			queue := record{PK: "TAX_QUEUE#SUBMITTED", SK: now.Add(-4*time.Minute).Format(time.RFC3339Nano) + "#" + approvedID, TaxRequest: &request, Status: taxStatusSubmitted}
			public := PublicTaxDocument{Folio: 381, IssueDate: "2026-11-10", TotalAmount: 29800, IssuedByIndomito: true, SIIStatus: "PENDING"}
			for _, row := range []record{
				{PK: "TAX_REQUEST#" + approvedID, SK: "META", TaxRequest: &request, Status: taxStatusSubmitted},
				queue,
				{PK: "TAX_QUEUE#RECORDED", SK: queue.SK, TaxRequest: &request, Status: taxStatusSubmitted},
				{PK: "TAX_PUBLIC#39#381", SK: "META", PublicTaxDocument: &public, Status: taxStatusSubmitted},
			} {
				saveLookupRow(t, db, row)
			}
			app := TaxSubmissionStatusApp{Accounts: service, Now: func() time.Time { return now }}
			if err := app.finish(context.Background(), queue, test.status); err != nil {
				t.Fatalf("finish: %v", err)
			}
			stored, err := service.read(context.Background(), "TAX_REQUEST#"+approvedID, "META")
			if err != nil || stored.TaxRequest == nil || stored.TaxRequest.Status != test.wantStatus || stored.TaxRequest.DeliveryStatus != "SMTP_ACCEPTED" {
				t.Fatalf("canonical state lost: %+v err=%v", stored.TaxRequest, err)
			}
			projection, err := service.read(context.Background(), "TAX_PUBLIC#39#381", "META")
			if err != nil || projection.PublicTaxDocument == nil || projection.PublicTaxDocument.SIIStatus != test.wantPublic || projection.PublicTaxDocument.SIIStatusUpdatedAt == "" {
				t.Fatalf("public projection not updated: %+v err=%v", projection.PublicTaxDocument, err)
			}
		})
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
