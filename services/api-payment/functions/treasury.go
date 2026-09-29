package functions

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/domain"
)

type forecastMonth struct {
	Month       string `json:"month"`
	Opening     int64  `json:"opening"`
	Collections int64  `json:"collections"`
	Expenses    int64  `json:"expenses"`
	Closing     int64  `json:"closing"`
}

func (a *App) treasury(w http.ResponseWriter, r *http.Request) {
	if !a.admin(r) {
		fail(w, 401)
		return
	}
	trip := r.URL.Query().Get("tripId")
	cash := domain.CalculateCashFlow(trip, 0, a.state.Installments, a.state.Payments, a.state.Expenses)
	forecasts := []forecastMonth{}
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	running := cash.Available
	for n := 0; n < 12; n++ {
		start := month.AddDate(0, n, 0)
		end := start.AddDate(0, 1, 0).Format(time.DateOnly)
		row := forecastMonth{Month: start.Format("2006-01"), Opening: running}
		for _, i := range a.state.Installments {
			if (trip == "" || i.TripID == trip) && i.DueDate < end && (n == 0 || i.DueDate >= start.Format(time.DateOnly)) {
				row.Collections += i.Amount - i.Paid
			}
		}
		if n == 0 {
			row.Collections += cash.InTransit
		}
		for _, e := range a.state.Expenses {
			if (trip == "" || e.TripID == trip) && e.DueDate < end && (n == 0 || e.DueDate >= start.Format(time.DateOnly)) {
				row.Expenses += e.Amount - e.Paid
			}
		}
		row.Closing = row.Opening + row.Collections - row.Expenses
		running = row.Closing
		forecasts = append(forecasts, row)
	}
	var sales, costs, fees int64
	payments := []domain.Payment{}
	expenses := []domain.Expense{}
	for _, i := range a.state.Installments {
		if trip == "" || i.TripID == trip {
			sales += i.Amount
		}
	}
	for _, e := range a.state.Expenses {
		if trip == "" || e.TripID == trip {
			costs += e.Amount
			expenses = append(expenses, e)
		}
	}
	for _, p := range a.state.Payments {
		if trip == "" || p.TripID == trip {
			payments = append(payments, p)
		}
		if (trip == "" || p.TripID == trip) && p.Status == "CONFIRMED" {
			fees += p.Fee
		}
	}
	write(w, 200, map[string]any{"cash": cash, "forecast": forecasts, "sales": sales, "budget": costs, "margin": sales - costs - fees, "payments": payments, "expenses": expenses, "events": a.state.Events, "mail": a.state.Mail})
}
func (a *App) expense(w http.ResponseWriter, r *http.Request) {
	if !a.admin(r) {
		fail(w, 401)
		return
	}
	var body struct {
		TripID   string `json:"tripId"`
		Category string `json:"category"`
		Supplier string `json:"supplier"`
		DueDate  string `json:"dueDate"`
		Amount   int64  `json:"amount"`
	}
	if decode(r, &body) != nil {
		fail(w, 400)
		return
	}
	if _, err := time.Parse(time.DateOnly, body.DueDate); err != nil {
		fail(w, 400)
		return
	}
	if body.Amount <= 0 || body.Amount > 1_000_000_000 || len(body.Supplier) > 120 || strings.TrimSpace(body.Supplier) == "" || len(body.Category) > 60 || body.Category == "" || (body.TripID != "gira-demo" && body.TripID != "company") {
		fail(w, 400)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 16 || len(key) > 100 {
		fail(w, 400)
		return
	}
	if previous := a.state.Keys["expense:"+key]; previous != "" {
		for _, e := range a.state.Expenses {
			if e.ID == previous && e.TripID == body.TripID && e.Amount == body.Amount && e.Supplier == body.Supplier && e.Category == body.Category && e.DueDate == body.DueDate {
				write(w, 200, e)
				return
			}
		}
		fail(w, 409)
		return
	}
	e := domain.Expense{ID: ulid.Make().String(), TripID: body.TripID, Category: body.Category, Supplier: body.Supplier, DueDate: body.DueDate, Amount: body.Amount}
	err := a.mutate(func() error {
		a.state.Expenses = append(a.state.Expenses, e)
		a.state.Keys["expense:"+key] = e.ID
		a.event("expense.committed", e.ID, e.TripID, e.Amount)
		return nil
	})
	if err != nil {
		fail(w, 500)
		return
	}
	write(w, 201, e)
}
func (a *App) payExpense(w http.ResponseWriter, r *http.Request) {
	if !a.admin(r) {
		fail(w, 401)
		return
	}
	var body struct {
		Amount int64 `json:"amount"`
	}
	if decode(r, &body) != nil || body.Amount <= 0 {
		fail(w, 400)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 16 || len(key) > 100 {
		fail(w, 400)
		return
	}
	fingerprint := r.PathValue("id") + ":" + strconv.FormatInt(body.Amount, 10)
	if previous := a.state.Keys["disbursement:"+key]; previous != "" {
		if previous != fingerprint {
			fail(w, 409)
			return
		}
		write(w, 200, map[string]bool{"ok": true})
		return
	}
	for index, e := range a.state.Expenses {
		if e.ID == r.PathValue("id") {
			if body.Amount > e.Amount-e.Paid {
				fail(w, 409)
				return
			}
			err := a.mutate(func() error {
				now := time.Now().UTC()
				a.state.Expenses[index].Paid += body.Amount
				a.state.Expenses[index].PaidAt = &now
				a.state.Keys["disbursement:"+key] = fingerprint
				a.event("expense.paid", e.ID, e.TripID, body.Amount)
				return nil
			})
			if err != nil {
				fail(w, 500)
				return
			}
			write(w, 200, a.state.Expenses[index])
			return
		}
	}
	fail(w, 404)
}
func (a *App) settle(w http.ResponseWriter, r *http.Request) {
	if !a.admin(r) {
		fail(w, 401)
		return
	}
	for index, p := range a.state.Payments {
		if p.ID == r.PathValue("id") {
			if p.Status != "CONFIRMED" {
				fail(w, 409)
				return
			}
			if p.SettledAt == nil {
				err := a.mutate(func() error {
					now := time.Now().UTC()
					a.state.Payments[index].SettledAt = &now
					a.event("payment.settled", p.ID, p.TripID, p.Amount-p.Fee)
					return nil
				})
				if err != nil {
					fail(w, 500)
					return
				}
			}
			write(w, 200, a.state.Payments[index])
			return
		}
	}
	fail(w, 404)
}
