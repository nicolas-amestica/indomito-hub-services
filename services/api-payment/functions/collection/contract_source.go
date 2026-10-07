package collection

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/libs/calendar"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// ContractSource es una frontera de lectura mínima; no importa ni modifica el dominio api-contract.
type ContractSource struct {
	DB    Database
	Table string
}

type approvedContract struct {
	ID      string `dynamodbav:"id"`
	Version int64  `dynamodbav:"version"`
	Status  string `dynamodbav:"status"`
	Content struct {
		PaymentPortal *struct {
			TripCode string `dynamodbav:"tripCode"`
		} `dynamodbav:"paymentPortal"`
		Plan struct {
			Name string `dynamodbav:"name"`
		} `dynamodbav:"plan"`
		Trip struct {
			DepartureDate string `dynamodbav:"departureDate"`
		} `dynamodbav:"trip"`
		Passengers []struct {
			Names     string `dynamodbav:"names"`
			LastNames string `dynamodbav:"lastNames"`
			DNI       string `dynamodbav:"dni"`
		} `dynamodbav:"passengers"`
		Payments struct {
			TotalPassengers   int   `dynamodbav:"totalPassengers"`
			FreePassengers    int   `dynamodbav:"freePassengers"`
			PricePerPerson    int64 `dynamodbav:"pricePerPerson"`
			DownPayment       int64 `dynamodbav:"downPayment"`
			DaysBeforePayment int   `dynamodbav:"daysBeforePayment"`
			Installments      struct {
				Quantity   int    `dynamodbav:"quantity"`
				StartMonth string `dynamodbav:"startMonth"`
				StartYear  int    `dynamodbav:"startYear"`
				StartDay   int    `dynamodbav:"startDay"`
			} `dynamodbav:"installments"`
			Conditions struct {
				RefundPolicyVersion int `dynamodbav:"refundPolicyVersion"`
			} `dynamodbav:"conditions"`
		} `dynamodbav:"payments"`
	} `dynamodbav:"content"`
}

// RosterMember pertenece a una participación, no a la identidad global del pasajero.
// Esta vista con RUT es exclusivamente administrativa y jamás debe usarse en el portal público.
type RosterMember struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	DNI  string `json:"dni"`
}

// SetupView devuelve condiciones aprobadas para revisión; el POST no acepta importes del navegador.
type SetupView struct {
	Status              string         `json:"status"`
	FreeParticipantIDs  []string       `json:"freeParticipantIds"`
	ContractID          string         `json:"contractId"`
	ContractVersion     int64          `json:"contractVersion"`
	TripName            string         `json:"tripName"`
	DepartureDate       string         `json:"departureDate"`
	FreeCount           int            `json:"freeCount"`
	PricePerPayer       int64          `json:"pricePerPayer"`
	DepositAgreed       int64          `json:"depositAgreed"`
	DueDates            []string       `json:"dueDates"`
	DaysBeforeDeparture int            `json:"daysBeforeDeparture"`
	Members             []RosterMember `json:"members"`
	PolicyVersion       int            `json:"policyVersion"`
	TripCode            string         `json:"-"`
}

// LoadApproved obtiene el contrato por clave y exige condiciones nuevas explícitas, sin migración implícita.
func (s ContractSource) LoadApproved(ctx context.Context, id string) (SetupView, error) {
	contractULID, err := ulid.ParseStrict(id)
	if err != nil || s.Table == "" || s.DB == nil {
		return SetupView{}, collection.ErrInvalid
	}
	out, err := s.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.Table, Key: key("CONTRACT#"+id, "METADATA"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return SetupView{}, fmt.Errorf("leer contrato aprobado: %w", err)
	}
	if len(out.Item) == 0 {
		return SetupView{}, ErrNotFound
	}
	var c approvedContract
	if err = attributevalue.UnmarshalMap(out.Item, &c); err != nil {
		return SetupView{}, fmt.Errorf("decodificar contrato: %w", err)
	}
	if c.ID != id || c.Status != "APPROVED" || c.Version < 1 || c.Content.Payments.Conditions.RefundPolicyVersion != 2 {
		return SetupView{}, collection.ErrConflict
	}
	p := c.Content.Payments
	i := p.Installments
	dates, err := calendar.MonthlyDates(i.StartYear, calendar.MonthNumber(i.StartMonth), i.StartDay, i.Quantity)
	if err != nil {
		return SetupView{}, collection.ErrInvalid
	}
	if len(c.Content.Passengers) < 1 || len(c.Content.Passengers) > 1000 || p.TotalPassengers < 1 || p.FreePassengers < 0 || p.TotalPassengers+p.FreePassengers != len(c.Content.Passengers) {
		return SetupView{}, collection.ErrInvalid
	}
	departure := ""
	if c.Content.Trip.DepartureDate != "" {
		date, parseErr := time.Parse(time.RFC3339Nano, c.Content.Trip.DepartureDate)
		if parseErr != nil {
			return SetupView{}, collection.ErrInvalid
		}
		departure = date.UTC().Format(time.DateOnly)
	}
	tripCode := ""
	if c.Content.PaymentPortal != nil {
		tripCode = strings.TrimSpace(c.Content.PaymentPortal.TripCode)
	}
	view := SetupView{ContractID: id, ContractVersion: c.Version, TripName: c.Content.Plan.Name, DepartureDate: departure, FreeCount: p.FreePassengers, PricePerPayer: p.PricePerPerson, DepositAgreed: p.DownPayment, DueDates: dates, DaysBeforeDeparture: p.DaysBeforePayment, PolicyVersion: 2, TripCode: tripCode, Members: []RosterMember{}}
	seen := map[string]bool{}
	for n, person := range c.Content.Passengers {
		rut := strings.ToUpper(strings.NewReplacer(".", "", " ", "").Replace(strings.TrimSpace(person.DNI)))
		if rut == "" || seen[rut] {
			return SetupView{}, collection.ErrInvalid
		}
		seen[rut] = true
		view.Members = append(view.Members, RosterMember{ID: participantID(contractULID, c.Version, n), Name: strings.TrimSpace(person.Names + " " + person.LastNames), DNI: rut})
	}
	return view, nil
}

func participantID(id ulid.ULID, version int64, index int) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("participation:v1:%s:%d:%d", id.String(), version, index)))
	copy(id[6:], hash[:10])
	return id.String()
}

// Startup fija liberados sobre la nómina original; rechaza IDs desconocidos o repetidos.
func (v SetupView) Startup(version int64, freeIDs []string) (collection.Startup, error) {
	if version != v.ContractVersion || len(freeIDs) != v.FreeCount || v.PolicyVersion != 2 {
		return collection.Startup{}, collection.ErrConflict
	}
	selected := map[string]bool{}
	for _, id := range freeIDs {
		if id == "" || selected[id] {
			return collection.Startup{}, collection.ErrInvalid
		}
		selected[id] = true
	}
	s := collection.Startup{TripID: v.ContractID, ContractID: v.ContractID, ContractVersion: v.ContractVersion, Approved: true, PricePerPayer: v.PricePerPayer, DepositAgreed: v.DepositAgreed, FreeCount: v.FreeCount, DueDates: v.DueDates, DepartureDate: v.DepartureDate, DaysBeforeDeparture: v.DaysBeforeDeparture}
	for _, member := range v.Members {
		free := selected[member.ID]
		delete(selected, member.ID)
		s.Participants = append(s.Participants, collection.Participant{ID: member.ID, Free: free, Name: member.Name, Document: member.DNI})
	}
	if len(selected) > 0 {
		return collection.Startup{}, collection.ErrInvalid
	}
	return s, nil
}
