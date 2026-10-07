package collection

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestAnnexReplacementImpactAndImmutableMoney(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	ctx := context.Background()
	old, next := input.Withdrawals[0].AccountID, input.Admissions[0].AccountID
	input.Replacements = []AnnexReplacement{{OutgoingAccountID: old, IncomingAccountID: next}}
	input.Admissions[0].Installments[0].Amount = 10000
	if _, err := s.PrepareAnnexDraft(ctx, input); err != nil {
		t.Fatal(err)
	}
	commits := db.commits
	impact, err := s.SummarizeAnnexDraft(ctx, input.TripID, input.ID)
	if err != nil || impact.Proposals != 2 || impact.Admissions != 1 || impact.Withdrawals != 1 || impact.Replacements != 1 || impact.DebtAdded != 40000 || impact.DebtRemoved != 50000 || impact.ReceivableDelta != -10000 || impact.StaleAccounts != 0 {
		t.Fatalf("impact: %+v %v", impact, err)
	}
	page, err := s.PreviewAnnexDraft(ctx, input.TripID, input.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Items {
		if (row.AccountID == old && row.RelatedAccountID != next) || (row.AccountID == next && row.RelatedAccountID != old) {
			t.Fatalf("missing bidirectional link: %+v", row)
		}
	}
	a, err := s.GetAccount(ctx, old)
	if err != nil || !a.Active || a.DepositReceived != 0 || a.Installments[0].Original != 20000 || db.commits != commits {
		t.Fatalf("preview changed money or installments: %+v %v", a, err)
	}
	if _, err := s.GetAccount(ctx, next); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replacement published: %v", err)
	}
	input.Replacements = nil
	if _, err := s.PrepareAnnexDraft(ctx, input); !errors.Is(err, ErrReplayMismatch) {
		t.Fatalf("relationship changed silently: %v", err)
	}
}

func TestAnnexReplacementRejectsInvalidPairsBeforeWriting(t *testing.T) {
	for _, scenario := range []string{"unknown outgoing", "unknown incoming", "self", "duplicate", "two replacements for one departure"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, input := annexDraftFixture(t)
			r := AnnexReplacement{OutgoingAccountID: input.Withdrawals[0].AccountID, IncomingAccountID: input.Admissions[0].AccountID}
			input.Replacements = []AnnexReplacement{r}
			switch scenario {
			case "unknown outgoing":
				input.Replacements[0].OutgoingAccountID = paymentOperationID("unknown")
			case "unknown incoming":
				input.Replacements[0].IncomingAccountID = paymentOperationID("unknown")
			case "self":
				input.Replacements[0].IncomingAccountID = r.OutgoingAccountID
			case "duplicate":
				input.Replacements = append(input.Replacements, r)
			case "two replacements for one departure":
				a := input.Admissions[0]
				a.AccountID = paymentOperationID("second")
				a.ParticipantID = a.AccountID
				input.Admissions = append(input.Admissions, a)
				input.Replacements = append(input.Replacements, AnnexReplacement{OutgoingAccountID: r.OutgoingAccountID, IncomingAccountID: a.AccountID})
			}
			if _, err := s.PrepareAnnexDraft(context.Background(), input); !errors.Is(err, domain.ErrInvalid) || db.commits != 0 {
				t.Fatalf("invalid relation wrote draft: %v commits=%d", err, db.commits)
			}
		})
	}
}

func TestAnnexImpactAggregatesAllPagesAndFreeParticipants(t *testing.T) {
	s, _, input := annexDraftFixture(t)
	input.Admissions = nil
	for i := 0; i < 45; i++ {
		id := paymentOperationID(fmt.Sprintf("impact-%d", i))
		a := domain.Admission{AccountID: id, ParticipantID: id, TripID: input.TripID, AnnexID: input.ID, Free: i < 5}
		if !a.Free {
			a.DepositAgreed = 1000
			a.Installments = []domain.AgreedInstallment{{ID: "01", DueDate: "2027-01-05", Amount: 2000}}
		}
		input.Admissions = append(input.Admissions, a)
	}
	if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	impact, err := s.SummarizeAnnexDraft(context.Background(), input.TripID, input.ID)
	if err != nil || impact.Proposals != 46 || impact.Admissions != 45 || impact.FreeAdmissions != 5 || impact.Withdrawals != 1 || impact.Replacements != 0 || impact.DebtAdded != 120000 || impact.DebtRemoved != 50000 || impact.ReceivableDelta != 70000 {
		t.Fatalf("incomplete totals: %+v %v", impact, err)
	}
}

func TestAnnexImpactRejectsMissingOrBrokenProposals(t *testing.T) {
	for _, scenario := range []string{"missing row", "broken relation", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, input := annexDraftFixture(t)
			old, next := input.Withdrawals[0].AccountID, input.Admissions[0].AccountID
			input.Replacements = []AnnexReplacement{{OutgoingAccountID: old, IncomingAccountID: next}}
			if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			pk, sk := "TRIP#"+input.TripID, "ANNEX#"+input.ID+"#PROPOSAL#"+next
			switch scenario {
			case "missing row":
				delete(db.items, pk+"/"+sk)
			case "broken relation":
				row, err := s.read(context.Background(), pk, sk)
				if err != nil {
					t.Fatal(err)
				}
				row.RelatedAccountID = ""
				saveLookupRow(t, db, row)
			case "stale":
				row, err := s.read(context.Background(), "ACCOUNT#"+old, "META")
				if err != nil {
					t.Fatal(err)
				}
				row.Version++
				row.Account.Version++
				saveLookupRow(t, db, row)
			}
			impact, err := s.SummarizeAnnexDraft(context.Background(), input.TripID, input.ID)
			if scenario == "stale" {
				if err != nil || impact.StaleAccounts != 1 {
					t.Fatalf("staleness lost: %+v %v", impact, err)
				}
			} else if !errors.Is(err, domain.ErrInvalid) || impact.Proposals != 0 {
				t.Fatalf("partial/corrupt summary returned: %+v %v", impact, err)
			}
		})
	}
}

type failingAnnexQuery struct{ *transactionDB }

func (d failingAnnexQuery) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return nil, errors.New("query unavailable")
}

func TestAnnexImpactDoesNotReturnTotalsAfterReadFailure(t *testing.T) {
	s, db, input := annexDraftFixture(t)
	if _, err := s.PrepareAnnexDraft(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	s.DB = failingAnnexQuery{db}
	if result, err := s.SummarizeAnnexDraft(context.Background(), input.TripID, input.ID); err == nil || result.Proposals != 0 {
		t.Fatalf("failed query returned totals: %+v %v", result, err)
	}
}
