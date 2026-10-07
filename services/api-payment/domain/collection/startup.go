package collection

import (
	"fmt"
	"sort"
	"time"
)

// Participant identifica explícitamente quién paga durante la puesta en marcha.
type Participant struct {
	ID       string `json:"id"`
	Free     bool   `json:"free"`
	Name     string `json:"name,omitempty"`
	Document string `json:"document,omitempty"`
}

// Startup contiene la instantánea aprobada y el calendario explícitamente acordado.
type Startup struct {
	LookupKeys          map[string]string `json:"lookupKeys,omitempty"`
	TripID              string            `json:"tripId"`
	ContractID          string            `json:"contractId"`
	ContractVersion     int64             `json:"contractVersion"`
	Approved            bool              `json:"approved"`
	PricePerPayer       int64             `json:"pricePerPayer"`
	DepositAgreed       int64             `json:"depositAgreed"`
	FreeCount           int               `json:"freeCount"`
	Participants        []Participant     `json:"participants"`
	DueDates            []string          `json:"dueDates"`
	DepartureDate       string            `json:"departureDate"`
	DaysBeforeDeparture int               `json:"daysBeforeDeparture"`
}

// Start genera cuentas deterministas; no registra dinero recibido ni activa un plan parcialmente publicado.
func Start(s Startup) ([]Account, error) {
	if !s.Approved || s.TripID == "" || s.ContractID == "" || s.ContractVersion < 1 || s.PricePerPayer < 1 || s.PricePerPayer > MaxAmount || s.DepositAgreed < 0 || s.FreeCount < 0 || len(s.Participants) < 1 || len(s.Participants) > 1000 || len(s.DueDates) < 1 || len(s.DueDates) > 120 || s.DaysBeforeDeparture < 0 || s.DaysBeforeDeparture > 365 {
		return nil, ErrInvalid
	}
	previous := ""
	for _, d := range s.DueDates {
		if !validDate(d) || d <= previous {
			return nil, ErrInvalid
		}
		previous = d
	}
	if s.DepartureDate != "" {
		departure, err := time.Parse(time.DateOnly, s.DepartureDate)
		if err != nil || previous > departure.AddDate(0, 0, -s.DaysBeforeDeparture).Format(time.DateOnly) {
			return nil, ErrInvalid
		}
	}
	people := append([]Participant(nil), s.Participants...)
	sort.Slice(people, func(i, j int) bool { return people[i].ID < people[j].ID })
	free := 0
	for i, p := range people {
		if p.ID == "" || (i > 0 && p.ID == people[i-1].ID) {
			return nil, ErrInvalid
		}
		if p.Free {
			free++
		}
	}
	payers := len(people) - free
	if free != s.FreeCount || payers < 1 || s.PricePerPayer > MaxAmount/int64(payers) || s.DepositAgreed > s.PricePerPayer*int64(payers) {
		return nil, ErrInvalid
	}
	accounts := make([]Account, 0, len(people))
	payer := 0
	for _, p := range people {
		a := Account{ID: p.ID, TripID: s.TripID, ParticipantID: p.ID, Version: 1, Active: true, Free: p.Free, Installments: []Installment{}}
		if !p.Free {
			a.DepositAgreed = share(s.DepositAgreed, payers, payer)
			balance := s.PricePerPayer - a.DepositAgreed
			for q, d := range s.DueDates {
				a.Installments = append(a.Installments, Installment{ID: fmt.Sprintf("%04d", q+1), DueDate: d, Original: share(balance, len(s.DueDates), q)})
			}
			payer++
		}
		if err := a.Validate(); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, nil
}

func share(total int64, count, index int) int64 {
	value := total / int64(count)
	if int64(index) < total%int64(count) {
		value++
	}
	return value
}
