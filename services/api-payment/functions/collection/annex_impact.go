package collection

import (
	"context"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// AnnexImpact resume exclusivamente el cambio propuesto, no el saldo total del viaje.
// ReceivableDelta no es caja ni devolución aprobada. Las versiones se observan
// secuencialmente: StaleAccounts=0 no autoriza aplicar sin revalidación transaccional.
type AnnexImpact struct {
	Proposals       int   `json:"proposals"`
	Admissions      int   `json:"admissions"`
	Withdrawals     int   `json:"withdrawals"`
	Replacements    int   `json:"replacements"`
	FreeAdmissions  int   `json:"freeAdmissions"`
	FreeWithdrawals int   `json:"freeWithdrawals"`
	StaleAccounts   int   `json:"staleAccounts"`
	DebtAdded       int64 `json:"debtAdded"`
	DebtRemoved     int64 `json:"debtRemoved"`
	ReceivableDelta int64 `json:"receivableDelta"`
}

// SummarizeAnnexDraft recorre páginas del anexo a pedido, sin escrituras ni Scan.
// Límite: 1000 propuestas; no debe usarse como sondeo periódico. No suma otras giras
// ni presenta una página parcial como resumen completo ante una falla.
func (s Service) SummarizeAnnexDraft(ctx context.Context, tripID, id string) (AnnexImpact, error) {
	state, err := s.GetAnnexDraft(ctx, tripID, id)
	if err != nil {
		return AnnexImpact{}, err
	}
	if state.Status != "DRAFT" || state.Prepared != state.Expected || state.Expected < 1 || state.Expected > 1000 {
		return AnnexImpact{}, domain.ErrConflict
	}
	rows := map[string]AnnexProposalSummary{}
	cursor := ""
	for {
		page, err := s.PreviewAnnexDraft(ctx, tripID, id, cursor)
		if err != nil {
			return AnnexImpact{}, err
		}
		for _, row := range page.Items {
			if _, exists := rows[row.AccountID]; exists {
				return AnnexImpact{}, domain.ErrInvalid
			}
			rows[row.AccountID] = row
			if int64(len(rows)) > state.Expected {
				return AnnexImpact{}, domain.ErrInvalid
			}
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor <= cursor || len(page.Items) == 0 {
			return AnnexImpact{}, domain.ErrInvalid
		}
		cursor = page.NextCursor
	}
	if int64(len(rows)) != state.Expected {
		return AnnexImpact{}, domain.ErrInvalid
	}
	result := AnnexImpact{Proposals: len(rows)}
	for _, row := range rows {
		// Cada cuenta válida limita su deuda a abono + cuotas, ambos <= MaxAmount.
		// Con hasta 1000 propuestas, estos acumulados también son exactos en JS.
		if row.DebtAdded < 0 || row.DebtAdded > 2*domain.MaxAmount || row.DebtRemoved < 0 || row.DebtRemoved > 2*domain.MaxAmount {
			return AnnexImpact{}, domain.ErrInvalid
		}
		result.DebtAdded += row.DebtAdded
		result.DebtRemoved += row.DebtRemoved
		if row.Stale {
			result.StaleAccounts++
		}
		if row.Kind == "PARTICIPANT_ADMITTED" {
			result.Admissions++
			if row.Free {
				result.FreeAdmissions++
			}
		} else {
			result.Withdrawals++
			if row.Free {
				result.FreeWithdrawals++
			}
		}
		if row.RelatedAccountID != "" {
			other, ok := rows[row.RelatedAccountID]
			if !ok || other.RelatedAccountID != row.AccountID || other.Kind == row.Kind {
				return AnnexImpact{}, domain.ErrInvalid
			}
			if row.Kind == "PARTICIPANT_ADMITTED" {
				result.Replacements++
			}
		}
	}
	result.ReceivableDelta = result.DebtAdded - result.DebtRemoved
	return result, nil
}
