package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

type Status string

const (
	StatusDraft           Status = "DRAFT"
	StatusPendingApproval Status = "PENDING_APPROVAL"
	StatusApproved        Status = "APPROVED"
	StatusRejected        Status = "REJECTED"
	StatusCancelled       Status = "CANCELLED"
)

func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusPendingApproval, StatusApproved, StatusRejected, StatusCancelled:
		return true
	default:
		return false
	}
}

func CanTransition(from, to Status) bool {
	if from == StatusApproved || from == StatusCancelled {
		return false
	}
	switch from {
	case StatusDraft:
		return to == StatusDraft || to == StatusPendingApproval || to == StatusCancelled
	case StatusPendingApproval:
		return to == StatusPendingApproval || to == StatusApproved || to == StatusRejected || to == StatusCancelled
	case StatusRejected:
		return to == StatusRejected || to == StatusDraft || to == StatusCancelled
	default:
		return false
	}
}

type Person struct {
	Name   string `json:"name" dynamodbav:"name"`
	DNI    string `json:"dni" dynamodbav:"dni"`
	Course string `json:"course,omitempty" dynamodbav:"course,omitempty"`
}

type Institution struct {
	Name    string `json:"name" dynamodbav:"name"`
	Address string `json:"address" dynamodbav:"address"`
}
type Trip struct {
	City           string `json:"city" dynamodbav:"city"`
	ContractDate   string `json:"contractDate" dynamodbav:"contractDate"`
	Destination    string `json:"destination" dynamodbav:"destination"`
	DepartureDate  string `json:"departureDate" dynamodbav:"departureDate"`
	ReturnDate     string `json:"returnDate" dynamodbav:"returnDate"`
	Days           int    `json:"days" dynamodbav:"days"`
	Nights         int    `json:"nights" dynamodbav:"nights"`
	DeparturePoint string `json:"departurePoint" dynamodbav:"departurePoint"`
}
type Service struct {
	Description string `json:"description" dynamodbav:"description"`
}
type Plan struct {
	Name             string    `json:"name" dynamodbav:"name"`
	ServicesIncluded []Service `json:"servicesIncluded" dynamodbav:"servicesIncluded"`
}
type Installments struct {
	Quantity                   int    `json:"quantity" dynamodbav:"quantity"`
	GroupInstallmentValue      int64  `json:"groupInstallmentValue" dynamodbav:"groupInstallmentValue"`
	IndividualInstallmentValue int64  `json:"individualInstallmentValue" dynamodbav:"individualInstallmentValue"`
	StartMonth                 string `json:"startMonth" dynamodbav:"startMonth"`
}
type Conditions struct {
	DepositPercentageWithFlight    int   `json:"depositPercentageWithFlight" dynamodbav:"depositPercentageWithFlight"`
	DepositPercentageWithoutFlight int   `json:"depositPercentageWithoutFlight" dynamodbav:"depositPercentageWithoutFlight"`
	SpecialProgramDeposit          int64 `json:"specialProgramDeposit" dynamodbav:"specialProgramDeposit"`
	DaysBeforeFlightBalance        int   `json:"daysBeforeFlightBalance" dynamodbav:"daysBeforeFlightBalance"`
	DaysBeforeTerrestrialBalance   int   `json:"daysBeforeTerrestrialBalance" dynamodbav:"daysBeforeTerrestrialBalance"`
	CancellationPenaltyPercentage  int   `json:"cancellationPenaltyPercentage" dynamodbav:"cancellationPenaltyPercentage"`
	CancellationNoticeDays         int   `json:"cancellationNoticeDays" dynamodbav:"cancellationNoticeDays"`
	ComplaintDeadlineDays          int   `json:"complaintDeadlineDays" dynamodbav:"complaintDeadlineDays"`
}
type BankAccount struct {
	AccountNumber string `json:"accountNumber" dynamodbav:"accountNumber"`
	AccountHolder string `json:"accountHolder" dynamodbav:"accountHolder"`
	HolderDNI     string `json:"holderDNI" dynamodbav:"holderDNI"`
	Bank          string `json:"bank" dynamodbav:"bank"`
	Email         string `json:"email" dynamodbav:"email"`
}
type Payments struct {
	TotalPassengers   int          `json:"totalPassengers" dynamodbav:"totalPassengers"`
	FreePassengers    int          `json:"freePassengers" dynamodbav:"freePassengers"`
	PricePerPerson    int64        `json:"pricePerPerson" dynamodbav:"pricePerPerson"`
	PriceInUSD        float64      `json:"priceInUSD" dynamodbav:"priceInUSD"`
	TotalGroup        int64        `json:"totalGroup" dynamodbav:"totalGroup"`
	DownPayment       int64        `json:"downPayment" dynamodbav:"downPayment"`
	GroupBalance      int64        `json:"groupBalance" dynamodbav:"groupBalance"`
	DaysBeforePayment int          `json:"daysBeforePayment" dynamodbav:"daysBeforePayment"`
	MaxExchangeRate   int64        `json:"maxExchangeRate" dynamodbav:"maxExchangeRate"`
	Installments      Installments `json:"installments" dynamodbav:"installments"`
	Conditions        Conditions   `json:"conditions" dynamodbav:"conditions"`
	BankAccount       BankAccount  `json:"bankAccount" dynamodbav:"bankAccount"`
}
type Passenger struct {
	Names       string `json:"names" dynamodbav:"names"`
	LastNames   string `json:"lastNames" dynamodbav:"lastNames"`
	DNI         string `json:"dni" dynamodbav:"dni"`
	BirthDate   string `json:"birthDate" dynamodbav:"birthDate"`
	Nationality string `json:"nationality" dynamodbav:"nationality"`
}
type Content struct {
	Representatives       []Person    `json:"representatives" dynamodbav:"representatives"`
	Institution           Institution `json:"institution" dynamodbav:"institution"`
	ClientRepresentatives []Person    `json:"clientRepresentatives" dynamodbav:"clientRepresentatives"`
	Trip                  Trip        `json:"trip" dynamodbav:"trip"`
	Plan                  Plan        `json:"plan" dynamodbav:"plan"`
	Payments              Payments    `json:"payments" dynamodbav:"payments"`
	Passengers            []Passenger `json:"passengers" dynamodbav:"passengers"`
}

type Contract struct {
	ID        string  `json:"id" dynamodbav:"id"`
	ProgramID string  `json:"programId,omitempty" dynamodbav:"programId,omitempty"`
	Period    string  `json:"period" dynamodbav:"period"`
	Status    Status  `json:"status" dynamodbav:"status"`
	Content   Content `json:"content" dynamodbav:"content"`
	CreatedAt string  `json:"createdAt" dynamodbav:"createdAt"`
	UpdatedAt string  `json:"updatedAt" dynamodbav:"updatedAt"`
	Version   int     `json:"version" dynamodbav:"version"`
}
type Item struct {
	PK          string `json:"-" dynamodbav:"pk"`
	SK          string `json:"-" dynamodbav:"sk"`
	GSIPeriodPK string `json:"-" dynamodbav:"gsiPeriodPk"`
	GSIPeriodSK string `json:"-" dynamodbav:"gsiPeriodSk"`
	Contract
}

func NewID() string       { return ulid.Make().String() }
func PK(id string) string { return "CONTRACT#" + id }

const SK = "METADATA"

func PeriodPK(period string) string { return "CONTRACT#PERIOD#" + period }
func YearPK(period string) string {
	year := strings.TrimSpace(period)
	if len(year) >= 4 {
		year = year[:4]
	}
	return "CONTRACT#YEAR#" + year
}
func NewItem(id, programID, period string, content Content, now time.Time) (Item, error) {
	if len(content.Passengers) == 0 {
		return Item{}, errors.New("la lista de pasajeros es obligatoria")
	}
	if strings.TrimSpace(period) == "" {
		period = now.Format("2006-01")
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	c := Contract{ID: id, ProgramID: strings.TrimSpace(programID), Period: period, Status: StatusDraft, Content: content, CreatedAt: stamp, UpdatedAt: stamp, Version: 1}
	return Item{PK: PK(id), SK: SK, GSIPeriodPK: YearPK(period), GSIPeriodSK: stamp + "#" + id, Contract: c}, nil
}
