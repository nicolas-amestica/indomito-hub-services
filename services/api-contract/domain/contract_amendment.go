package domain

import (
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

type ContractAmendmentStatus string

const (
	ContractAmendmentDraft    ContractAmendmentStatus = "DRAFT"
	ContractAmendmentApproved ContractAmendmentStatus = "APPROVED"
)

// ContractTermsSnapshot contiene únicamente condiciones operativas que pueden
// cambiar por anexo. Deliberadamente no contiene precios, cuotas ni pagos.
type ContractTermsSnapshot struct {
	DepartureDate string    `json:"departureDate" dynamodbav:"departureDate"`
	ReturnDate    string    `json:"returnDate" dynamodbav:"returnDate"`
	Days          int       `json:"days" dynamodbav:"days"`
	Nights        int       `json:"nights" dynamodbav:"nights"`
	Services      []Service `json:"services" dynamodbav:"services"`
}

type ContractAmendment struct {
	ID                  string                  `json:"id" dynamodbav:"id"`
	ContractID          string                  `json:"contractId" dynamodbav:"contractId"`
	BaseContractVersion int                     `json:"baseContractVersion" dynamodbav:"baseContractVersion"`
	BaseTermsRevision   int                     `json:"baseTermsRevision" dynamodbav:"baseTermsRevision"`
	Version             int                     `json:"version" dynamodbav:"version"`
	Status              ContractAmendmentStatus `json:"status" dynamodbav:"status"`
	Reason              string                  `json:"reason" dynamodbav:"reason"`
	Before              ContractTermsSnapshot   `json:"before" dynamodbav:"before"`
	After               ContractTermsSnapshot   `json:"after" dynamodbav:"after"`
	CreatedAt           string                  `json:"createdAt" dynamodbav:"createdAt"`
	CreatedBy           string                  `json:"createdBy" dynamodbav:"createdBy"`
	ApprovedAt          string                  `json:"approvedAt,omitempty" dynamodbav:"approvedAt,omitempty"`
	ApprovedBy          string                  `json:"approvedBy,omitempty" dynamodbav:"approvedBy,omitempty"`
	PDFDocument         *PDFDocument            `json:"pdfDocument,omitempty" dynamodbav:"pdfDocument,omitempty"`
}

type ContractAmendmentItem struct {
	PK string `json:"-" dynamodbav:"pk"`
	SK string `json:"-" dynamodbav:"sk"`
	ContractAmendment
}

func AmendmentSK(id string) string { return "AMENDMENT#" + id }

func NewContractAmendmentItem(amendment ContractAmendment) ContractAmendmentItem {
	return ContractAmendmentItem{PK: PK(amendment.ContractID), SK: AmendmentSK(amendment.ID), ContractAmendment: amendment}
}

func TermsSnapshot(content Content) ContractTermsSnapshot {
	services := append([]Service(nil), content.Plan.ServicesIncluded...)
	return ContractTermsSnapshot{
		DepartureDate: content.Trip.DepartureDate,
		ReturnDate:    content.Trip.ReturnDate,
		Days:          content.Trip.Days,
		Nights:        content.Trip.Nights,
		Services:      services,
	}
}

func NewContractAmendment(id string, contract Contract, before ContractTermsSnapshot, baseTermsRevision int, after ContractTermsSnapshot, reason, actor string, now time.Time) (ContractAmendment, error) {
	reason, actor = strings.TrimSpace(reason), strings.TrimSpace(actor)
	if _, err := ulid.ParseStrict(id); err != nil || contract.Status != StatusApproved || contract.ID == "" || contract.Version < 1 || baseTermsRevision < 0 || len(reason) < 5 || len(reason) > 500 || actor == "" || now.IsZero() {
		return ContractAmendment{}, errors.New("anexo contractual no valido")
	}
	normalized, err := normalizeTermsSnapshot(after)
	if err != nil {
		return ContractAmendment{}, err
	}
	normalizedBefore, err := normalizeTermsSnapshot(before)
	if err != nil || reflect.DeepEqual(normalizedBefore, normalized) {
		return ContractAmendment{}, errors.New("el anexo debe modificar fechas o servicios")
	}
	return ContractAmendment{
		ID: id, ContractID: contract.ID, BaseContractVersion: contract.Version, BaseTermsRevision: baseTermsRevision,
		Status: ContractAmendmentDraft, Version: 1, Reason: reason, Before: normalizedBefore, After: normalized,
		CreatedAt: now.UTC().Format(time.RFC3339Nano), CreatedBy: actor,
	}, nil
}

func ApproveContractAmendment(amendment ContractAmendment, actor string, now time.Time) (ContractAmendment, error) {
	actor = strings.TrimSpace(actor)
	if amendment.Status != ContractAmendmentDraft || actor == "" || now.IsZero() {
		return ContractAmendment{}, errors.New("anexo contractual no puede aprobarse")
	}
	amendment.Status = ContractAmendmentApproved
	amendment.Version++
	amendment.ApprovedAt = now.UTC().Format(time.RFC3339Nano)
	amendment.ApprovedBy = actor
	return amendment, nil
}

func normalizeTermsSnapshot(snapshot ContractTermsSnapshot) (ContractTermsSnapshot, error) {
	var err error
	if snapshot.DepartureDate, err = normalizeDateUTC(snapshot.DepartureDate); err != nil {
		return ContractTermsSnapshot{}, errors.New("fecha de salida no valida")
	}
	if snapshot.ReturnDate, err = normalizeDateUTC(snapshot.ReturnDate); err != nil {
		return ContractTermsSnapshot{}, errors.New("fecha de retorno no valida")
	}
	if (snapshot.DepartureDate == "") != (snapshot.ReturnDate == "") || snapshot.Days < 1 || snapshot.Nights < 0 {
		return ContractTermsSnapshot{}, errors.New("rango de viaje incompleto")
	}
	if snapshot.DepartureDate != "" {
		departure, departureErr := time.Parse(time.RFC3339Nano, snapshot.DepartureDate)
		returnDate, returnErr := time.Parse(time.RFC3339Nano, snapshot.ReturnDate)
		if departureErr != nil || returnErr != nil || returnDate.Before(departure) || int(returnDate.Sub(departure).Hours()/24)+1 != snapshot.Days {
			return ContractTermsSnapshot{}, errors.New("rango de viaje no coincide con la cantidad de dias")
		}
	}
	if len(snapshot.Services) == 0 || len(snapshot.Services) > 200 {
		return ContractTermsSnapshot{}, errors.New("servicios del anexo no validos")
	}
	for index := range snapshot.Services {
		snapshot.Services[index].Description = strings.TrimSpace(snapshot.Services[index].Description)
		if snapshot.Services[index].Description == "" || len(snapshot.Services[index].Description) > 1000 {
			return ContractTermsSnapshot{}, errors.New("servicios del anexo no validos")
		}
	}
	return snapshot, nil
}
