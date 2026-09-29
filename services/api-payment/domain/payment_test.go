package domain

import (
	"testing"
	"time"
)

func TestSplitBalancePreservesExactContract(t *testing.T) {
	for _, balance := range []int64{0, 1, 3000000, 3000001, 10000003} {
		rows, err := SplitBalance(balance, 30, 5)
		if err != nil {
			t.Fatal(err)
		}
		var sum int64
		for _, row := range rows {
			for _, value := range row {
				sum += value
			}
		}
		if sum != balance {
			t.Fatalf("saldo %d se convirtió en %d", balance, sum)
		}
	}
}
func TestCashDistinguishesConfirmationFromSettlement(t *testing.T) {
	payments := []Payment{{Amount: 20000, Fee: 164, Status: "CONFIRMED", TripID: "trip"}}
	installments := []Installment{{TripID: "trip", Amount: 100000, Paid: 20000}}
	expenses := []Expense{{TripID: "trip", Amount: 60000, Paid: 10000}}
	cash := CalculateCashFlow("trip", 10000, installments, payments, expenses)
	if cash.Available != 0 || cash.InTransit != 19836 || cash.Receivables != 80000 || cash.Payables != 50000 {
		t.Fatalf("caja incorrecta: %+v", cash)
	}
	now := time.Now()
	payments[0].SettledAt = &now
	cash = CalculateCashFlow("trip", 10000, installments, payments, expenses)
	if cash.Available != 19836 || cash.InTransit != 0 {
		t.Fatalf("liquidación incorrecta: %+v", cash)
	}
}
