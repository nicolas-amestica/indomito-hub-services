package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// AnnexWithdrawal fija la versión que el operador revisó, no una baja incondicional.
type AnnexWithdrawal struct {
	AccountID       string `json:"accountId"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

// AnnexReplacement vincula una baja y un alta sin transferir dinero entre cuentas.
type AnnexReplacement struct {
	OutgoingAccountID string `json:"outgoingAccountId"`
	IncomingAccountID string `json:"incomingAccountId"`
}

// AnnexDraftInput debe construirse en una capa administrativa autorizada.
// Audit identifica al operador; no se debe aceptar su identidad desde el navegador.
type AnnexDraftInput struct {
	Replacements []AnnexReplacement       `json:"replacements,omitempty"`
	LookupKeys   map[string]string        `json:"lookupKeys,omitempty"`
	Profiles     map[string]AnnexIdentity `json:"profiles,omitempty"`
	ID           string                   `json:"id"`
	TripID       string                   `json:"tripId"`
	Audit        domain.Audit             `json:"audit"`
	Withdrawals  []AnnexWithdrawal        `json:"withdrawals"`
	Admissions   []domain.Admission       `json:"admissions"`
}

type AnnexIdentity struct {
	Name     string `json:"name"`
	Document string `json:"document"`
}

// AnnexDraftState describe preparación, no aprobación contractual ni aplicación financiera.
type AnnexDraftState struct {
	ID             string `json:"id"`
	TripID         string `json:"tripId"`
	Status         string `json:"status"`
	Prepared       int64  `json:"prepared"`
	Expected       int64  `json:"expected"`
	ApprovalReason string `json:"approvalReason,omitempty"`
}

func canonicalDraft(input AnnexDraftInput) (AnnexDraftInput, string, error) {
	if !validPortalID(input.ID) || !validPortalID(input.TripID) || input.Audit.CommandID != input.ID || input.Audit.Actor == "" || len(input.Audit.Actor) > 256 || input.Audit.Reason == "" || len(input.Audit.Reason) > 2000 || input.Audit.RecordedAt.IsZero() || len(input.Withdrawals)+len(input.Admissions) < 1 || len(input.Withdrawals)+len(input.Admissions) > 1000 {
		return input, "", domain.ErrInvalid
	}
	input.Withdrawals = append([]AnnexWithdrawal(nil), input.Withdrawals...)
	input.Admissions = append([]domain.Admission(nil), input.Admissions...)
	input.Replacements = append([]AnnexReplacement(nil), input.Replacements...)
	sort.Slice(input.Replacements, func(i, j int) bool {
		return input.Replacements[i].OutgoingAccountID < input.Replacements[j].OutgoingAccountID
	})
	sort.Slice(input.Withdrawals, func(i, j int) bool { return input.Withdrawals[i].AccountID < input.Withdrawals[j].AccountID })
	sort.Slice(input.Admissions, func(i, j int) bool { return input.Admissions[i].AccountID < input.Admissions[j].AccountID })
	seen := map[string]bool{}
	people := map[string]bool{}
	admissionAccounts := map[string]bool{}
	lookups := map[string]bool{}
	for _, withdrawal := range input.Withdrawals {
		if !validPortalID(withdrawal.AccountID) || withdrawal.ExpectedVersion < 1 || seen[withdrawal.AccountID] {
			return input, "", domain.ErrInvalid
		}
		seen[withdrawal.AccountID] = true
	}
	for _, admission := range input.Admissions {
		if admission.TripID != input.TripID || admission.AnnexID != input.ID || seen[admission.AccountID] || people[admission.ParticipantID] || !validPortalID(admission.AccountID) || !validPortalID(admission.ParticipantID) {
			return input, "", domain.ErrInvalid
		}
		for _, installment := range admission.Installments {
			if len(installment.ID) > 128 {
				return input, "", domain.ErrInvalid
			}
		}
		if _, err := domain.Admit(admission, input.Audit); err != nil {
			return input, "", err
		}
		seen[admission.AccountID], people[admission.ParticipantID] = true, true
		admissionAccounts[admission.AccountID] = true
		if profile, exists := input.Profiles[admission.AccountID]; exists {
			if strings.TrimSpace(profile.Name) == "" || len(profile.Name) > 200 || strings.TrimSpace(profile.Document) == "" || len(profile.Document) > 64 {
				return input, "", domain.ErrInvalid
			}
		}
		if lookup, exists := input.LookupKeys[admission.AccountID]; exists {
			if len(lookup) != 68 || !strings.HasPrefix(lookup, "RUT#") || lookups[lookup] {
				return input, "", domain.ErrInvalid
			}
			if _, err := hex.DecodeString(lookup[4:]); err != nil {
				return input, "", domain.ErrInvalid
			}
			lookups[lookup] = true
		}
	}
	if len(lookups) != len(input.LookupKeys) {
		return input, "", domain.ErrInvalid
	}
	for accountID := range input.Profiles {
		if !admissionAccounts[accountID] {
			return input, "", domain.ErrInvalid
		}
	}
	if _, err := replacementLinks(input); err != nil {
		return input, "", err
	}
	b, err := json.Marshal(input)
	if err != nil {
		return input, "", err
	}
	digest := sha256.Sum256(b)
	return input, hex.EncodeToString(digest[:]), nil
}

// PrepareAnnexDraft guarda propuestas por cuenta, sin cambiar nómina, deuda, dinero
// ni accesos públicos. Cada transacción tiene como máximo tres acciones.
// Repetir la misma entrada recupera una interrupción; una entrada distinta requiere
// otro ID. Un borrador puede quedar obsoleto: aprobar exige revalidar todas las
// versiones e identidades bajo exclusión, funcionalidad separada de este método.
func (s Service) PrepareAnnexDraft(ctx context.Context, input AnnexDraftInput) (AnnexDraftState, error) {
	if s.DB == nil || s.Table == "" {
		return AnnexDraftState{}, domain.ErrInvalid
	}
	input, hash, err := canonicalDraft(input)
	if err != nil {
		return AnnexDraftState{}, err
	}
	root := record{PK: "TRIP#" + input.TripID, SK: "ANNEX#" + input.ID, TripID: input.TripID, Version: 1, Status: "PREPARING_DRAFT", Expected: int64(len(input.Withdrawals) + len(input.Admissions)), Fingerprint: hash,
		Event: &domain.Event{Audit: input.Audit, SchemaVersion: 1, Type: "ANNEX_DRAFT_REQUESTED", TripID: input.TripID, Reference: input.ID}}
	if err = s.beginAnnexDraft(ctx, root); err != nil {
		return AnnexDraftState{}, err
	}
	links, err := replacementLinks(input)
	if err != nil {
		return AnnexDraftState{}, err
	}
	for _, withdrawal := range input.Withdrawals {
		if err = s.prepareAnnexProposal(ctx, root, withdrawal.AccountID, links[withdrawal.AccountID], "", AnnexIdentity{}, func() (domain.Change, error) {
			a, readErr := s.GetAccount(ctx, withdrawal.AccountID)
			if readErr != nil {
				return domain.Change{}, readErr
			}
			if a.TripID != input.TripID || a.Version != withdrawal.ExpectedVersion {
				return domain.Change{}, domain.ErrConflict
			}
			audit := input.Audit
			audit.CommandID = paymentOperationID(input.ID + ":" + a.ID)
			return domain.Withdraw(a, audit, input.ID)
		}); err != nil {
			return AnnexDraftState{}, err
		}
	}
	for _, admission := range input.Admissions {
		if err = s.prepareAnnexProposal(ctx, root, admission.AccountID, links[admission.AccountID], input.LookupKeys[admission.AccountID], input.Profiles[admission.AccountID], func() (domain.Change, error) {
			if _, readErr := s.read(ctx, "ACCOUNT#"+admission.AccountID, "META"); readErr == nil {
				return domain.Change{}, domain.ErrConflict
			} else if !errors.Is(readErr, ErrNotFound) {
				return domain.Change{}, readErr
			}
			audit := input.Audit
			audit.CommandID = paymentOperationID(input.ID + ":" + admission.AccountID)
			return domain.Admit(admission, audit)
		}); err != nil {
			return AnnexDraftState{}, err
		}
	}
	if err = s.advanceAnnexDraft(ctx, root, nil); err != nil {
		return AnnexDraftState{}, err
	}
	return s.GetAnnexDraft(ctx, input.TripID, input.ID)
}

// GetAnnexDraft lee metadatos por clave; no publica propuestas ni enumera otras giras.
func (s Service) GetAnnexDraft(ctx context.Context, tripID, id string) (AnnexDraftState, error) {
	if !validPortalID(tripID) || !validPortalID(id) || s.DB == nil || s.Table == "" {
		return AnnexDraftState{}, domain.ErrInvalid
	}
	root, err := s.read(ctx, "TRIP#"+tripID, "ANNEX#"+id)
	if err != nil {
		return AnnexDraftState{}, err
	}
	state := AnnexDraftState{ID: id, TripID: tripID, Status: root.Status, Prepared: root.Prepared, Expected: root.Expected}
	if root.Approval != nil {
		state.ApprovalReason = root.Approval.Reason
	}
	return state, nil
}

func (s Service) beginAnnexDraft(ctx context.Context, root record) error {
	if old, err := s.read(ctx, root.PK, root.SK); err == nil {
		if old.Fingerprint != root.Fingerprint {
			return ErrReplayMismatch
		}
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	write, err := s.put(root, "attribute_not_exists(pk)", nil)
	if err != nil {
		return err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.openRosterCheck(root.TripID), write}})
	if err != nil {
		if old, readErr := s.read(ctx, root.PK, root.SK); readErr == nil {
			if old.Fingerprint == root.Fingerprint {
				return nil
			}
			return ErrReplayMismatch
		}
		var cancelled *types.TransactionCanceledException
		if errors.As(err, &cancelled) {
			return domain.ErrConflict
		}
	}
	return err
}

func (s Service) prepareAnnexProposal(ctx context.Context, root record, accountID, relatedAccountID, lookup string, identity AnnexIdentity, build func() (domain.Change, error)) error {
	proposal := record{PK: root.PK, SK: root.SK + "#PROPOSAL#" + accountID, Fingerprint: root.Fingerprint, AccountID: accountID, RelatedAccountID: relatedAccountID, LookupKey: lookup}
	if old, err := s.read(ctx, proposal.PK, proposal.SK); err == nil {
		if old.Fingerprint != root.Fingerprint {
			return ErrReplayMismatch
		}
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	change, err := build()
	if err != nil {
		return err
	}
	proposal.Change = &change
	profile := RosterProjection{AccountID: change.Account.ID, ParticipantID: change.Account.ParticipantID, Name: strings.TrimSpace(identity.Name), Document: strings.TrimSpace(identity.Document), Active: change.Account.Active, Free: change.Account.Free, Version: change.Account.Version}
	if change.Event.Type == "PARTICIPANT_WITHDRAWN" {
		member, readErr := s.read(ctx, root.PK, "MEMBER#"+accountID)
		if readErr == nil && member.Roster != nil {
			profile = *member.Roster
			profile.Active, profile.Free, profile.Version = change.Account.Active, change.Account.Free, change.Account.Version
		}
		profile.NextParticipation = relatedAccountID
	} else {
		profile.PreviousParticipation = relatedAccountID
	}
	proposal.Roster = &profile
	return s.advanceAnnexDraft(ctx, root, &proposal)
}

func (s Service) advanceAnnexDraft(ctx context.Context, identity record, proposal *record) error {
	var lastErr error
	for retry := 0; retry < 4; retry++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		root, err := s.read(ctx, identity.PK, identity.SK)
		if err != nil {
			return err
		}
		if root.Fingerprint != identity.Fingerprint {
			return ErrReplayMismatch
		}
		if proposal != nil {
			if old, readErr := s.read(ctx, proposal.PK, proposal.SK); readErr == nil {
				if old.Fingerprint != root.Fingerprint {
					return ErrReplayMismatch
				}
				return nil
			} else if !errors.Is(readErr, ErrNotFound) {
				return readErr
			}
		} else if root.Status == "DRAFT" {
			return nil
		}
		if root.Status != "PREPARING_DRAFT" || root.Expected < 1 || root.Prepared > root.Expected {
			return domain.ErrConflict
		}
		previous := root.Version
		root.Version++
		if proposal == nil {
			if root.Prepared != root.Expected {
				return domain.ErrConflict
			}
			root.Status = "DRAFT"
		} else {
			if root.Prepared == root.Expected {
				return domain.ErrConflict
			}
			root.Prepared++
		}
		write, err := s.put(root, "#version = :previous", versionValue(previous))
		if err != nil {
			return err
		}
		writes := []types.TransactWriteItem{s.openRosterCheck(root.TripID), write}
		if proposal != nil {
			row, buildErr := s.put(*proposal, "attribute_not_exists(pk)", nil)
			if buildErr != nil {
				return buildErr
			}
			writes = append(writes, row)
		}
		_, lastErr = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
		if lastErr == nil {
			return nil
		}
	}
	var cancelled *types.TransactionCanceledException
	if errors.As(lastErr, &cancelled) {
		return domain.ErrConflict
	}
	return lastErr
}
