// Package analytics valida planificación operativa sin ejecutar cobros ni copiar el motor comercial.
package analytics

import (
	"encoding/json"
	"errors"
	"github.com/oklog/ulid/v2"
	"math"
	"strings"
	"time"
)

// ErrInvalid indica un presupuesto o comando inválido.
var ErrInvalid = errors.New("presupuesto inválido")

// Category es una clasificación configurable, nunca inferida del nombre de proveedor.
type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Allocation relaciona parcialmente un costo con un compromiso, sin sumarlo nuevamente.
type Allocation struct {
	SupplierID string  `json:"supplierId"`
	Amount     float64 `json:"amount"`
}

// Tier cambia el precio unitario al alcanzar una cantidad mínima de viajeros.
type Tier struct {
	From      int     `json:"from"`
	UnitPrice float64 `json:"unitPrice"`
}

// Line conserva moneda, multiplicadores y procedencia histórica explícita.
type Line struct {
	ID           string       `json:"id"`
	Description  string       `json:"description"`
	CategoryID   string       `json:"categoryId"`
	SupplierID   string       `json:"supplierId"`
	ChargeType   string       `json:"chargeType"`
	Basis        string       `json:"basis"`
	Quantity     float64      `json:"quantity"`
	UnitPrice    float64      `json:"unitPrice"`
	Currency     string       `json:"currency"`
	Rate         float64      `json:"rate"`
	RateSource   string       `json:"rateSource"`
	RateDate     string       `json:"rateDate"`
	Buffer       float64      `json:"buffer"`
	Days         int          `json:"days"`
	Nights       int          `json:"nights"`
	Minimum      int          `json:"minimum"`
	Capacity     int          `json:"capacity"`
	Included     bool         `json:"included"`
	State        string       `json:"state"`
	Note         string       `json:"note"`
	Tiers        []Tier       `json:"tiers"`
	Allocations  []Allocation `json:"allocations"`
	ProviderName string       `json:"providerName"`
}

// Flow es un movimiento previsto; no se registra en tesorería.
type Flow struct {
	Date        string `json:"date"`
	Description string `json:"description"`
	Amount      int64  `json:"amount"`
}

// Source conserva identidad y versión de la referencia importada.
type Source struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Version string `json:"version"`
	Date    string `json:"date"`
}

// Taxes es el snapshot explícito de configuración tributaria y base de costos.
type Taxes struct {
	VatRate             float64 `json:"vatRate"`
	CrewWithholdingRate float64 `json:"crewWithholdingRate"`
	Source              string  `json:"source"`
	CostBasis           string  `json:"costBasis"`
}

// Budget es una entrada del simulador, no una instrucción financiera.
type Budget struct {
	Name             string     `json:"name"`
	TotalPassengers  int        `json:"totalPassengers"`
	FreePassengers   int        `json:"freePassengers"`
	Days             int        `json:"days"`
	Nights           int        `json:"nights"`
	PricePerPayer    int64      `json:"pricePerPayer"`
	Discount         float64    `json:"discount"`
	Contingency      float64    `json:"contingency"`
	TargetMode       string     `json:"targetMode"`
	Target           float64    `json:"target"`
	Source           Source     `json:"source"`
	Taxes            Taxes      `json:"taxes"`
	Categories       []Category `json:"categories"`
	Lines            []Line     `json:"lines"`
	Flows            []Flow     `json:"flows"`
	ForecastComplete bool       `json:"forecastComplete"`
}

// Audit conserva la identidad autorizada y el motivo de cada revisión.
type Audit struct {
	Actor  string `json:"actor"`
	At     string `json:"at"`
	Reason string `json:"reason"`
	Action string `json:"action"`
}

// State conserva estimación, línea base y escenarios independientes.
type State struct {
	Version   int64             `json:"version"`
	Current   *Budget           `json:"current"`
	Baseline  *Budget           `json:"baseline"`
	Scenarios map[string]Budget `json:"scenarios"`
	Audit     Audit             `json:"audit"`
}

// Command expresa una sola intención versionada e idempotente.
type Command struct {
	CommandID  string  `json:"commandId"`
	Version    int64   `json:"version"`
	Action     string  `json:"action"`
	ScenarioID string  `json:"scenarioId"`
	Reason     string  `json:"reason"`
	Budget     *Budget `json:"budget"`
}

func finite(n float64, min, max float64) bool {
	return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= min && n <= max
}
func id(s string) bool { _, err := ulid.ParseStrict(s); return err == nil }
func one(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

// Validate rechaza monedas, cantidades, tasas y relaciones inconsistentes.
func (b Budget) Validate() error {
	if len(strings.TrimSpace(b.Name)) < 2 || len(b.Name) > 150 || b.TotalPassengers < 1 || b.TotalPassengers > 1000 || b.FreePassengers < 0 || b.FreePassengers >= b.TotalPassengers || b.Days < 1 || b.Days > 365 || b.Nights < 0 || b.Nights > 365 || b.PricePerPayer < 0 || b.PricePerPayer > 1e10 || !finite(b.Discount, 0, 100) || !finite(b.Contingency, 0, 100) || !finite(b.Target, 0, 1e12) || !one(b.TargetMode, "amount", "margin") || (b.TargetMode == "margin" && b.Target >= 100) || !finite(b.Taxes.VatRate, 0, 100) || !finite(b.Taxes.CrewWithholdingRate, 0, 100) || b.Taxes.Source == "" || b.Taxes.CostBasis != "net" || len(b.Lines) > 120 || len(b.Categories) < 1 || len(b.Categories) > 40 || len(b.Flows) > 240 {
		return ErrInvalid
	}
	categories := map[string]bool{}
	for _, c := range b.Categories {
		if !id(c.ID) || len(strings.TrimSpace(c.Name)) < 2 || len(c.Name) > 80 || categories[c.ID] {
			return ErrInvalid
		}
		categories[c.ID] = true
	}
	seen := map[string]bool{}
	for _, l := range b.Lines {
		if !id(l.ID) || seen[l.ID] || len(strings.TrimSpace(l.Description)) < 2 || len(l.Description) > 250 || !categories[l.CategoryID] || !one(l.Currency, "CLP", "USD", "BRL") || !finite(l.UnitPrice, 0, 1e10) || !finite(l.Quantity, 0, 10000) || !finite(l.Rate, 0.000001, 1e6) || !finite(l.Buffer, 0, 1e6) || (l.Currency == "CLP" && (l.Rate != 1 || l.Buffer != 0)) || l.RateSource == "" || !one(l.ChargeType, "fixed", "per_passenger", "per_passenger_night", "per_day", "per_passenger_day", "crew") || !one(l.Basis, "total", "paying", "free", "participants", "units") || !one(l.State, "estimated", "quoted", "committed", "confirmed") || l.Days < 0 || l.Days > 365 || l.Nights < 0 || l.Nights > 365 || l.Minimum < 0 || l.Minimum > 10000 || l.Capacity < 0 || l.Capacity > 10000 || len(l.ProviderName) > 150 || len(l.Note) > 1000 || len(l.Tiers) > 30 || len(l.Allocations) > 30 || (l.SupplierID != "" && !id(l.SupplierID)) {
			return ErrInvalid
		}
		if _, err := time.Parse(time.DateOnly, l.RateDate); err != nil {
			return ErrInvalid
		}
		seen[l.ID] = true
		previous := -1
		for _, t := range l.Tiers {
			if t.From <= previous || t.From > 1000 || !finite(t.UnitPrice, 0, 1e10) {
				return ErrInvalid
			}
			previous = t.From
		}
		allocated := map[string]bool{}
		for _, a := range l.Allocations {
			if !id(a.SupplierID) || allocated[a.SupplierID] || !finite(a.Amount, 0, 1e12) {
				return ErrInvalid
			}
			allocated[a.SupplierID] = true
		}
	}
	for _, f := range b.Flows {
		if _, err := time.Parse(time.DateOnly, f.Date); err != nil || len(f.Description) < 2 || len(f.Description) > 150 || f.Amount < -1e12 || f.Amount > 1e12 {
			return ErrInvalid
		}
	}
	if !one(b.Source.Kind, "manual", "contract", "quotation") || len(b.Source.ID) > 100 || len(b.Source.Version) > 100 || len(b.Taxes.Source) > 200 {
		return ErrInvalid
	}
	return nil
}

// Apply copia el estado y ejecuta aprobación/promoción explícitas, sin alterar snapshots anteriores.
func Apply(current State, c Command, actor string, now time.Time) (State, error) {
	if !id(c.CommandID) || c.Version != current.Version || len(strings.TrimSpace(c.Reason)) < 5 || len(c.Reason) > 500 || actor == "" {
		return State{}, ErrInvalid
	}
	raw, err := json.Marshal(current)
	if err != nil {
		return State{}, err
	}
	var next State
	if err = json.Unmarshal(raw, &next); err != nil {
		return State{}, err
	}
	if next.Scenarios == nil {
		next.Scenarios = map[string]Budget{}
	}
	switch c.Action {
	case "SAVE":
		if c.Budget == nil || c.Budget.Validate() != nil {
			return State{}, ErrInvalid
		}
		next.Current = c.Budget
	case "SCENARIO":
		if !id(c.ScenarioID) || c.Budget == nil || c.Budget.Validate() != nil {
			return State{}, ErrInvalid
		}
		if _, exists := next.Scenarios[c.ScenarioID]; !exists && len(next.Scenarios) >= 6 {
			return State{}, ErrInvalid
		}
		next.Scenarios[c.ScenarioID] = *c.Budget
	case "APPROVE":
		if current.Current == nil || current.Current.Validate() != nil || len(current.Current.Lines) == 0 {
			return State{}, ErrInvalid
		}
		next.Baseline = next.Current
	case "PROMOTE":
		b, ok := next.Scenarios[c.ScenarioID]
		if !ok {
			return State{}, ErrInvalid
		}
		next.Current = &b
	default:
		return State{}, ErrInvalid
	}
	next.Version++
	next.Audit = Audit{Actor: actor, At: now.UTC().Format(time.RFC3339), Reason: strings.TrimSpace(c.Reason), Action: c.Action}
	return next, nil
}

func hasIncludedLines(b Budget) bool {
	for _, line := range b.Lines {
		if line.Included {
			return true
		}
	}
	return false
}
