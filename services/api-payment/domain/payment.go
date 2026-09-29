// Package domain define obligaciones y tesorería del portal de pagos.
package domain

import (
	"errors"
	"time"
)

// Installment representa una obligación individual y su saldo en CLP.
type Installment struct {
	ID          string `json:"id"`
	TripID      string `json:"tripId"`
	TripName    string `json:"tripName"`
	PassengerID string `json:"passengerId"`
	Label       string `json:"label"`
	DueDate     string `json:"dueDate"`
	Amount      int64  `json:"amount"`
	Paid        int64  `json:"paid"`
	ReceiptID   string `json:"receiptId,omitempty"`
}

// Payment conserva bruto, comisión estimada de demostración y liquidación.
type Payment struct {
	ID             string     `json:"id"`
	InstallmentID  string     `json:"installmentId"`
	PassengerID    string     `json:"passengerId"`
	TripID         string     `json:"tripId"`
	Amount         int64      `json:"amount"`
	Fee            int64      `json:"fee"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"createdAt"`
	SettledAt      *time.Time `json:"settledAt,omitempty"`
	IdempotencyKey string     `json:"-"`
}

// Receipt es un comprobante interno; no es documento tributario.
type Receipt struct {
	ID          string    `json:"id"`
	PaymentID   string    `json:"paymentId"`
	PassengerID string    `json:"passengerId"`
	TripName    string    `json:"tripName"`
	Installment string    `json:"installment"`
	Amount      int64     `json:"amount"`
	IssuedAt    time.Time `json:"issuedAt"`
	Description string    `json:"description"`
}

// Expense representa un compromiso con proveedor por viaje.
type Expense struct {
	ID       string     `json:"id"`
	TripID   string     `json:"tripId"`
	Category string     `json:"category"`
	Supplier string     `json:"supplier"`
	DueDate  string     `json:"dueDate"`
	Amount   int64      `json:"amount"`
	Paid     int64      `json:"paid"`
	PaidAt   *time.Time `json:"paidAt,omitempty"`
}

// Event es la salida versionada e inmutable para auditoría y proyecciones BI.
type Event struct {
	ID          string    `json:"id"`
	Version     int       `json:"version"`
	Type        string    `json:"type"`
	AggregateID string    `json:"aggregateId"`
	TripID      string    `json:"tripId"`
	Amount      int64     `json:"amount"`
	Currency    string    `json:"currency"`
	OccurredAt  time.Time `json:"occurredAt"`
}

// Mail contiene una entrega capturada exclusivamente en el entorno local.
type Mail struct {
	ID        string    `json:"id"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// CashFlow separa efectivo, deuda y fondos en tránsito.
type CashFlow struct {
	Opening       int64 `json:"opening"`
	Receipts      int64 `json:"receipts"`
	Disbursements int64 `json:"disbursements"`
	Available     int64 `json:"available"`
	InTransit     int64 `json:"inTransit"`
	Receivables   int64 `json:"receivables"`
	Payables      int64 `json:"payables"`
	Projected     int64 `json:"projected"`
}

// SplitBalance distribuye pesos enteros sin crear ni perder saldo.
func SplitBalance(balance int64, passengers, quantity int) ([][]int64, error) {
	if balance < 0 || balance > 1_000_000_000_000 || passengers < 1 || passengers > 1000 || quantity < 1 || quantity > 120 {
		return nil, errors.New("plan fuera de límites")
	}
	result := make([][]int64, passengers)
	for p := range passengers {
		person := balance / int64(passengers)
		if int64(p) < balance%int64(passengers) {
			person++
		}
		result[p] = make([]int64, quantity)
		for q := range quantity {
			result[p][q] = person / int64(quantity)
			if int64(q) < person%int64(quantity) {
				result[p][q]++
			}
		}
	}
	return result, nil
}

// CalculateCashFlow calcula caja real y compromisos para un viaje o todos.
func CalculateCashFlow(tripID string, opening int64, installments []Installment, payments []Payment, expenses []Expense) CashFlow {
	c := CashFlow{Opening: opening}
	for _, i := range installments {
		if tripID == "" || i.TripID == tripID {
			c.Receivables += i.Amount - i.Paid
		}
	}
	for _, p := range payments {
		if (tripID != "" && p.TripID != tripID) || p.Status != "CONFIRMED" {
			continue
		}
		if p.SettledAt == nil {
			c.InTransit += p.Amount - p.Fee
		} else {
			c.Receipts += p.Amount - p.Fee
		}
	}
	for _, e := range expenses {
		if tripID != "" && e.TripID != tripID {
			continue
		}
		c.Payables += e.Amount - e.Paid
		c.Disbursements += e.Paid
	}
	c.Available = c.Opening + c.Receipts - c.Disbursements
	c.Projected = c.Available + c.InTransit + c.Receivables - c.Payables
	return c
}
