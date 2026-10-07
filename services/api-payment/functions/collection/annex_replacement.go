package collection

import domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"

func replacementLinks(input AnnexDraftInput) (map[string]string, error) {
	outgoing, incoming := map[string]bool{}, map[string]bool{}
	for _, w := range input.Withdrawals {
		outgoing[w.AccountID] = true
	}
	for _, a := range input.Admissions {
		incoming[a.AccountID] = true
	}
	links := map[string]string{}
	for _, r := range input.Replacements {
		if !outgoing[r.OutgoingAccountID] || !incoming[r.IncomingAccountID] || r.OutgoingAccountID == r.IncomingAccountID || links[r.OutgoingAccountID] != "" || links[r.IncomingAccountID] != "" {
			return nil, domain.ErrInvalid
		}
		links[r.OutgoingAccountID], links[r.IncomingAccountID] = r.IncomingAccountID, r.OutgoingAccountID
	}
	return links, nil
}
