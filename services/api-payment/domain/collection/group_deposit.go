package collection

import "sort"

type DepositCandidate struct {
	AccountID   string `json:"accountId"`
	Outstanding int64  `json:"outstanding"`
}

type DepositAllocation struct {
	AccountID string `json:"accountId"`
	Amount    int64  `json:"amount"`
}

// AllocateGroupDeposit reparte pesos enteros de forma equitativa y
// determinista, sin exceder el abono pendiente individual.
func AllocateGroupDeposit(amount int64, candidates []DepositCandidate) ([]DepositAllocation, error) {
	if amount <= 0 || len(candidates) == 0 || len(candidates) > 1000 {
		return nil, ErrInvalid
	}
	rows := append([]DepositCandidate(nil), candidates...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].AccountID < rows[j].AccountID })
	total := int64(0)
	const maxInt64 = int64(^uint64(0) >> 1)
	for index, row := range rows {
		if row.AccountID == "" || row.Outstanding <= 0 || row.Outstanding > MaxAmount || (index > 0 && row.AccountID == rows[index-1].AccountID) || total > maxInt64-row.Outstanding {
			return nil, ErrInvalid
		}
		total += row.Outstanding
	}
	if amount > total {
		return nil, ErrInvalid
	}
	allocated := make([]int64, len(rows))
	remaining := amount
	for remaining > 0 {
		eligible := make([]int, 0, len(rows))
		for index, row := range rows {
			if allocated[index] < row.Outstanding {
				eligible = append(eligible, index)
			}
		}
		if len(eligible) == 0 {
			return nil, ErrInvalid
		}
		base, extra := remaining/int64(len(eligible)), remaining%int64(len(eligible))
		if base == 0 {
			base = 1
			extra = 0
		}
		progress := int64(0)
		for position, index := range eligible {
			share := base
			if int64(position) < extra {
				share++
			}
			capacity := rows[index].Outstanding - allocated[index]
			if share > capacity {
				share = capacity
			}
			if share > remaining-progress {
				share = remaining - progress
			}
			allocated[index] += share
			progress += share
			if progress == remaining {
				break
			}
		}
		if progress == 0 {
			return nil, ErrInvalid
		}
		remaining -= progress
	}
	result := make([]DepositAllocation, 0, len(rows))
	for index, value := range allocated {
		if value > 0 {
			result = append(result, DepositAllocation{AccountID: rows[index].AccountID, Amount: value})
		}
	}
	return result, nil
}
