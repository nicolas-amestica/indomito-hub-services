package collection

import (
	"reflect"
	"testing"
)

func TestAllocateGroupDepositIsExactDeterministicAndCapped(t *testing.T) {
	candidates := []DepositCandidate{{AccountID: "c", Outstanding: 10}, {AccountID: "a", Outstanding: 2}, {AccountID: "b", Outstanding: 10}}
	got, err := AllocateGroupDeposit(13, candidates)
	if err != nil {
		t.Fatal(err)
	}
	want := []DepositAllocation{{AccountID: "a", Amount: 2}, {AccountID: "b", Amount: 6}, {AccountID: "c", Amount: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("allocation = %+v, want %+v", got, want)
	}
	reversed, err := AllocateGroupDeposit(13, []DepositCandidate{candidates[2], candidates[1], candidates[0]})
	if err != nil || !reflect.DeepEqual(reversed, want) {
		t.Fatalf("input order changed allocation: %+v %v", reversed, err)
	}
}

func TestAllocateGroupDepositRejectsUnassignableMoney(t *testing.T) {
	tests := []struct {
		name       string
		amount     int64
		candidates []DepositCandidate
	}{
		{name: "zero", candidates: []DepositCandidate{{AccountID: "a", Outstanding: 1}}},
		{name: "overpayment", amount: 3, candidates: []DepositCandidate{{AccountID: "a", Outstanding: 2}}},
		{name: "duplicate", amount: 1, candidates: []DepositCandidate{{AccountID: "a", Outstanding: 1}, {AccountID: "a", Outstanding: 1}}},
		{name: "no outstanding", amount: 1, candidates: []DepositCandidate{{AccountID: "a"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := AllocateGroupDeposit(test.amount, test.candidates); err == nil {
				t.Fatal("invalid allocation accepted")
			}
		})
	}
}
