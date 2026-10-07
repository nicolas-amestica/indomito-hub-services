package domain

import (
	"reflect"
	"testing"
	"time"
)

func approvedContractForAmendment(t *testing.T) Contract {
	t.Helper()
	content := validContent()
	content.Trip.DepartureDate = ""
	content.Trip.ReturnDate = ""
	return Contract{ID: NewID(), Version: 4, Status: StatusApproved, Content: content}
}

func TestContractAmendmentKeepsBeforeAfterWithoutPaymentFields(t *testing.T) {
	contract := approvedContractForAmendment(t)
	after := TermsSnapshot(contract.Content)
	after.DepartureDate = "2027-10-05"
	after.ReturnDate = "2027-10-08"
	after.Days = 4
	after.Nights = 3
	after.Services = append(after.Services, Service{Description: "  Excursión adicional  "})
	now := time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)
	amendment, err := NewContractAmendment(NewID(), contract, TermsSnapshot(contract.Content), 0, after, "Definir fecha y servicio adicional", "operator", now)
	if err != nil {
		t.Fatal(err)
	}
	if amendment.Status != ContractAmendmentDraft || amendment.Before.DepartureDate != "" || amendment.After.DepartureDate != "2027-10-05T00:00:00Z" || amendment.After.Services[len(amendment.After.Services)-1].Description != "Excursión adicional" {
		t.Fatalf("invalid snapshot: %+v", amendment)
	}
	approved, err := ApproveContractAmendment(amendment, "approver", now.Add(time.Hour))
	if err != nil || approved.Status != ContractAmendmentApproved || approved.ApprovedBy != "approver" || !reflect.DeepEqual(approved.Before, amendment.Before) || !reflect.DeepEqual(approved.After, amendment.After) {
		t.Fatalf("invalid approval: %+v %v", approved, err)
	}
}

func TestContractAmendmentRejectsNoopInvalidRangeAndUnapprovedBase(t *testing.T) {
	contract := approvedContractForAmendment(t)
	now := time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)
	if _, err := NewContractAmendment(NewID(), contract, TermsSnapshot(contract.Content), 0, TermsSnapshot(contract.Content), "Sin cambios reales", "operator", now); err == nil {
		t.Fatal("no-op amendment accepted")
	}
	after := TermsSnapshot(contract.Content)
	after.DepartureDate, after.ReturnDate = "2027-10-05", "2027-10-08"
	if _, err := NewContractAmendment(NewID(), contract, TermsSnapshot(contract.Content), 0, after, "Fechas inconsistentes", "operator", now); err == nil {
		t.Fatal("inconsistent range accepted")
	}
	contract.Status = StatusDraft
	after.Days, after.Nights = 4, 3
	if _, err := NewContractAmendment(NewID(), contract, TermsSnapshot(contract.Content), 0, after, "Contrato no aprobado", "operator", now); err == nil {
		t.Fatal("draft base accepted")
	}
}
