package functions

import (
	"errors"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/domain"
)

func (a *App) installments(w http.ResponseWriter, r *http.Request) {
	passenger := a.passenger(r)
	if passenger == "" {
		fail(w, 401)
		return
	}
	list := []domain.Installment{}
	for _, i := range a.state.Installments {
		if i.PassengerID == passenger {
			list = append(list, i)
		}
	}
	write(w, 200, list)
}
func (a *App) createPayment(w http.ResponseWriter, r *http.Request) {
	passenger := a.passenger(r)
	if passenger == "" {
		fail(w, 401)
		return
	}
	var body struct {
		InstallmentID string `json:"installmentId"`
	}
	key := r.Header.Get("Idempotency-Key")
	if decode(r, &body) != nil || len(key) < 16 || len(key) > 100 {
		fail(w, 400)
		return
	}
	for _, p := range a.state.Payments {
		if a.state.Keys[passenger+":"+key] == p.ID {
			if p.InstallmentID != body.InstallmentID {
				fail(w, 409)
				return
			}
			write(w, 200, p)
			return
		}
	}
	for _, i := range a.state.Installments {
		if i.ID != body.InstallmentID || i.PassengerID != passenger {
			continue
		}
		if i.Paid >= i.Amount {
			fail(w, 409)
			return
		}
		for _, p := range a.state.Payments {
			if p.InstallmentID == i.ID && p.Status == "PENDING" {
				if err := a.mutate(func() error {
					a.state.Keys[passenger+":"+key] = p.ID
					return nil
				}); err != nil {
					fail(w, 500)
					return
				}
				write(w, 200, p)
				return
			}
		}
		p := domain.Payment{ID: ulid.Make().String(), InstallmentID: i.ID, PassengerID: passenger, TripID: i.TripID, Amount: i.Amount - i.Paid, Status: "PENDING", CreatedAt: time.Now().UTC()}
		// Estimación de prueba: 0,69% + 19% de IVA. Se reemplaza por comisión conciliada.
		p.Fee = (p.Amount*8211 + 500000) / 1000000
		err := a.mutate(func() error {
			a.state.Payments = append(a.state.Payments, p)
			a.state.Keys[passenger+":"+key] = p.ID
			return nil
		})
		if err != nil {
			fail(w, 500)
			return
		}
		write(w, 201, p)
		return
	}
	fail(w, 404)
}
func (a *App) simulate(w http.ResponseWriter, r *http.Request) {
	passenger := a.passenger(r)
	if passenger == "" {
		fail(w, 401)
		return
	}
	var body struct {
		Outcome string `json:"outcome"`
	}
	if decode(r, &body) != nil || (body.Outcome != "CONFIRMED" && body.Outcome != "FAILED") {
		fail(w, 400)
		return
	}
	for index, p := range a.state.Payments {
		if p.ID != r.PathValue("id") || p.PassengerID != passenger {
			continue
		}
		if p.Status != "PENDING" {
			write(w, 200, p)
			return
		}
		err := a.mutate(func() error {
			a.state.Payments[index].Status = body.Outcome
			if body.Outcome == "FAILED" {
				a.event("payment.failed", p.ID, p.TripID, p.Amount)
				return nil
			}
			for j, i := range a.state.Installments {
				if i.ID != p.InstallmentID {
					continue
				}
				if i.Amount-i.Paid != p.Amount {
					return errors.New("saldo cambió")
				}
				receipt := domain.Receipt{ID: ulid.Make().String(), PaymentID: p.ID, PassengerID: p.PassengerID, TripName: i.TripName, Installment: i.Label, Amount: p.Amount, IssuedAt: time.Now().UTC(), Description: "COMPROBANTE DE PRUEBA — pago simulado, sin dinero real. No es boleta ni factura SII."}
				a.state.Receipts = append(a.state.Receipts, receipt)
				a.state.Installments[j].Paid += p.Amount
				a.state.Installments[j].ReceiptID = receipt.ID
				a.state.Mail = append(a.state.Mail, domain.Mail{ID: receipt.ID, Subject: "Comprobante de pago de prueba", Body: receipt.Description + " · " + i.TripName + " · " + i.Label + " · Comprobante " + receipt.ID, CreatedAt: receipt.IssuedAt})
				a.event("payment.confirmed", p.ID, p.TripID, p.Amount)
				a.event("receipt.issued", receipt.ID, p.TripID, p.Amount)
				return nil
			}
			return errors.New("cuota no encontrada")
		})
		if err != nil {
			fail(w, 409)
			return
		}
		write(w, 200, a.state.Payments[index])
		return
	}
	fail(w, 404)
}
func (a *App) receipt(w http.ResponseWriter, r *http.Request) {
	passenger := a.passenger(r)
	if passenger == "" {
		fail(w, 401)
		return
	}
	for _, v := range a.state.Receipts {
		if v.ID == r.PathValue("id") && v.PassengerID == passenger {
			write(w, 200, v)
			return
		}
	}
	fail(w, 404)
}
