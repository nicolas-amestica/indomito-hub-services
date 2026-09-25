package domain

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strconv"
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
	Course  string `json:"course" dynamodbav:"course"`
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
	Sex         string `json:"sex" dynamodbav:"sex"`
}

type ProgramReference struct {
	ID        string         `json:"id" dynamodbav:"id"`
	Name      string         `json:"name" dynamodbav:"name"`
	UpdatedAt string         `json:"updatedAt" dynamodbav:"updatedAt"`
	Content   map[string]any `json:"content" dynamodbav:"content"`
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

type PDFDocument struct {
	ObjectKey        string `json:"objectKey" dynamodbav:"objectKey"`
	ContentType      string `json:"contentType" dynamodbav:"contentType"`
	Size             int64  `json:"size" dynamodbav:"size"`
	SHA256           string `json:"sha256" dynamodbav:"sha256"`
	GeneratorVersion string `json:"generatorVersion" dynamodbav:"generatorVersion"`
	GeneratedAt      string `json:"generatedAt" dynamodbav:"generatedAt"`
	GeneratedBy      string `json:"generatedBy" dynamodbav:"generatedBy"`
}

type StatusAuditItem struct {
	PK         string `dynamodbav:"pk"`
	SK         string `dynamodbav:"sk"`
	Entity     string `dynamodbav:"entity"`
	ContractID string `dynamodbav:"contractId"`
	From       Status `dynamodbav:"from"`
	To         Status `dynamodbav:"to"`
	ChangedBy  string `dynamodbav:"changedBy"`
	ChangedAt  string `dynamodbav:"changedAt"`
	Version    int    `dynamodbav:"version"`
}

type Contract struct {
	ID               string            `json:"id" dynamodbav:"id"`
	ProgramID        string            `json:"programId,omitempty" dynamodbav:"programId,omitempty"`
	ProgramReference *ProgramReference `json:"programReference,omitempty" dynamodbav:"programReference,omitempty"`
	Period           string            `json:"period" dynamodbav:"period"`
	Status           Status            `json:"status" dynamodbav:"status"`
	Content          Content           `json:"content" dynamodbav:"content"`
	CreatedAt        string            `json:"createdAt" dynamodbav:"createdAt"`
	UpdatedAt        string            `json:"updatedAt" dynamodbav:"updatedAt"`
	Version          int               `json:"version" dynamodbav:"version"`
	PDFDocument      *PDFDocument      `json:"pdfDocument,omitempty" dynamodbav:"pdfDocument,omitempty"`
	ApprovedAt       string            `json:"approvedAt,omitempty" dynamodbav:"approvedAt,omitempty"`
	ApprovedBy       string            `json:"approvedBy,omitempty" dynamodbav:"approvedBy,omitempty"`
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
func NewItem(id, programID string, programReference *ProgramReference, period string, content Content, now time.Time) (Item, error) {
	NormalizePayments(&content)
	if err := NormalizeDates(&content, programReference); err != nil {
		return Item{}, err
	}
	if err := ValidateContent(content); err != nil {
		return Item{}, err
	}
	programID = strings.TrimSpace(programID)
	if programID == "" || programReference == nil {
		return Item{}, errors.New("el programa guardado y su referencia son obligatorios")
	}
	if programReference != nil {
		if programID == "" {
			programID = strings.TrimSpace(programReference.ID)
		}
		if programID == "" || programID != strings.TrimSpace(programReference.ID) {
			return Item{}, errors.New("la referencia del programa no coincide con programId")
		}
	}
	if strings.TrimSpace(period) == "" {
		return Item{}, errors.New("el periodo es obligatorio")
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	c := Contract{ID: id, ProgramID: programID, ProgramReference: programReference, Period: period, Status: StatusDraft, Content: content, CreatedAt: stamp, UpdatedAt: stamp, Version: 1}
	return Item{PK: PK(id), SK: SK, GSIPeriodPK: YearPK(period), GSIPeriodSK: stamp + "#" + id, Contract: c}, nil
}

func NormalizeDates(content *Content, programReference *ProgramReference) error {
	fields := []struct {
		name  string
		value *string
	}{
		{"fecha del contrato", &content.Trip.ContractDate},
		{"fecha de salida", &content.Trip.DepartureDate},
		{"fecha de retorno", &content.Trip.ReturnDate},
	}
	for index := range content.Passengers {
		fields = append(fields, struct {
			name  string
			value *string
		}{fmt.Sprintf("fecha de nacimiento del pasajero %d", index+1), &content.Passengers[index].BirthDate})
	}
	if programReference != nil {
		fields = append(fields, struct {
			name  string
			value *string
		}{"fecha de actualizacion del programa", &programReference.UpdatedAt})
	}
	for _, field := range fields {
		normalized, err := normalizeDateUTC(*field.value)
		if err != nil {
			return fmt.Errorf("%s no es valida", field.name)
		}
		*field.value = normalized
	}
	return nil
}

func normalizeDateUTC(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if instant, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return instant.UTC().Format(time.RFC3339Nano), nil
	}
	for _, layout := range []string{"2006-01-02", "02/01/2006", "02-01-2006"} {
		if date, err := time.Parse(layout, value); err == nil {
			return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339), nil
		}
	}
	return "", errors.New("fecha invalida")
}

func NormalizePayments(content *Content) {
	passengerCount := len(content.Passengers)
	maxFree := passengerCount - 1
	if maxFree < 0 {
		maxFree = 0
	}
	if content.Payments.FreePassengers < 0 {
		content.Payments.FreePassengers = 0
	} else if content.Payments.FreePassengers > maxFree {
		content.Payments.FreePassengers = maxFree
	}
	content.Payments.TotalPassengers = passengerCount - content.Payments.FreePassengers
	if content.Payments.PricePerPerson < 0 {
		content.Payments.PricePerPerson = 0
	}
	content.Payments.TotalGroup = int64(content.Payments.TotalPassengers) * content.Payments.PricePerPerson
	content.Payments.GroupBalance = content.Payments.TotalGroup - content.Payments.DownPayment
	if content.Payments.GroupBalance < 0 {
		content.Payments.GroupBalance = 0
	}
	quantity := int64(content.Payments.Installments.Quantity)
	if quantity <= 0 {
		content.Payments.Installments.GroupInstallmentValue = 0
		content.Payments.Installments.IndividualInstallmentValue = 0
		return
	}
	content.Payments.Installments.GroupInstallmentValue = ceilDivision(content.Payments.GroupBalance, quantity)
	content.Payments.Installments.IndividualInstallmentValue = ceilDivision(content.Payments.GroupBalance, quantity*int64(content.Payments.TotalPassengers))
}

func ceilDivision(value, divisor int64) int64 {
	if value <= 0 || divisor <= 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}

var rutPattern = regexp.MustCompile(`^(\d{7,8})-([0-9K])$`)

func ValidateContent(content Content) error {
	if err := validatePeople("representante de Giras Indomito", content.Representatives, false); err != nil {
		return err
	}
	if err := validatePeople("representante del cliente", content.ClientRepresentatives, true); err != nil {
		return err
	}
	if strings.TrimSpace(content.Institution.Name) == "" || strings.TrimSpace(content.Institution.Address) == "" || strings.TrimSpace(content.Institution.Course) == "" {
		return errors.New("los datos de la institucion son obligatorios")
	}
	trip := content.Trip
	if strings.TrimSpace(trip.City) == "" || strings.TrimSpace(trip.Destination) == "" || strings.TrimSpace(trip.DeparturePoint) == "" || trip.ContractDate == "" || trip.DepartureDate == "" || trip.ReturnDate == "" || trip.Days < 1 || trip.Nights < 0 {
		return errors.New("los datos del viaje son obligatorios")
	}
	departure, departureErr := time.Parse(time.RFC3339Nano, trip.DepartureDate)
	returnDate, returnErr := time.Parse(time.RFC3339Nano, trip.ReturnDate)
	if departureErr != nil || returnErr != nil || int(returnDate.Sub(departure).Hours()/24)+1 != trip.Days {
		return errors.New("el rango de viaje debe coincidir exactamente con la cantidad de dias")
	}
	if strings.TrimSpace(content.Plan.Name) == "" || len(content.Plan.ServicesIncluded) == 0 {
		return errors.New("el programa y sus servicios son obligatorios")
	}
	for _, service := range content.Plan.ServicesIncluded {
		if strings.TrimSpace(service.Description) == "" {
			return errors.New("todos los servicios incluidos son obligatorios")
		}
	}
	payment := content.Payments
	if payment.PricePerPerson <= 0 || payment.MaxExchangeRate <= 0 || payment.Installments.Quantity < 1 || strings.TrimSpace(payment.Installments.StartMonth) == "" {
		return errors.New("los datos de pago y cuotas son obligatorios")
	}
	if strings.TrimSpace(payment.BankAccount.AccountNumber) == "" || strings.TrimSpace(payment.BankAccount.AccountHolder) == "" || strings.TrimSpace(payment.BankAccount.Bank) == "" || !ValidRUT(payment.BankAccount.HolderDNI) {
		return errors.New("la cuenta bancaria no es valida")
	}
	if _, err := mail.ParseAddress(payment.BankAccount.Email); err != nil {
		return errors.New("el email de la cuenta bancaria no es valido")
	}
	if len(content.Passengers) == 0 {
		return errors.New("la lista de pasajeros es obligatoria")
	}
	for index, passenger := range content.Passengers {
		if strings.TrimSpace(passenger.Names) == "" || strings.TrimSpace(passenger.LastNames) == "" || strings.TrimSpace(passenger.Nationality) == "" {
			return fmt.Errorf("el pasajero %d tiene datos obligatorios incompletos", index+1)
		}
		if !ValidRUT(passenger.DNI) {
			return fmt.Errorf("el RUT del pasajero %d no es valido", index+1)
		}
		if !validBirthDate(passenger.BirthDate) {
			return fmt.Errorf("la fecha de nacimiento del pasajero %d no es valida", index+1)
		}
		switch passenger.Sex {
		case "FEMALE", "MALE", "OTHER", "NOT_SPECIFIED":
		default:
			return fmt.Errorf("el sexo del pasajero %d no es valido", index+1)
		}
	}
	return nil
}

func validatePeople(label string, people []Person, courseRequired bool) error {
	if len(people) == 0 {
		return fmt.Errorf("debe existir al menos un %s", label)
	}
	for index, person := range people {
		if strings.TrimSpace(person.Name) == "" || !ValidRUT(person.DNI) || (courseRequired && strings.TrimSpace(person.Course) == "") {
			return fmt.Errorf("el %s %d tiene datos obligatorios incompletos o un RUT no valido", label, index+1)
		}
	}
	return nil
}

func ValidRUT(value string) bool {
	cleaned := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(value, ".", ""), " ", ""))
	match := rutPattern.FindStringSubmatch(cleaned)
	if match == nil {
		return false
	}
	sum, factor := 0, 2
	for index := len(match[1]) - 1; index >= 0; index-- {
		digit, _ := strconv.Atoi(string(match[1][index]))
		sum += digit * factor
		factor++
		if factor == 8 {
			factor = 2
		}
	}
	result := 11 - sum%11
	expected := strconv.Itoa(result)
	if result == 11 {
		expected = "0"
	} else if result == 10 {
		expected = "K"
	}
	return expected == match[2]
}

func validBirthDate(value string) bool {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02", "02/01/2006", "02-01-2006"} {
		date, err := time.Parse(layout, strings.TrimSpace(value))
		if err == nil {
			return date.Year() >= 1920 && !date.After(time.Now())
		}
	}
	return false
}
