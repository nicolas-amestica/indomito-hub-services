package collection

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// ApplyAnnex prevalida, aplica cada propuesta una vez y publica el resultado
// completo. El contrato aprobado original permanece inmutable.
func (s Service) ApplyAnnex(ctx context.Context, tripID, annexID string) error {
	root, err := s.read(ctx, "TRIP#"+tripID, "ANNEX#"+annexID)
	if err != nil || root.Event == nil {
		return domain.ErrInvalid
	}
	return s.ApplyAnnexWithApproval(ctx, tripID, annexID, root.Event.Audit)
}

// ApplyAnnexWithApproval conserva por separado la identidad y motivo de quien
// confirma el anexo. Reintentar exige exactamente la misma auditoría.
func (s Service) ApplyAnnexWithApproval(ctx context.Context, tripID, annexID string, approval domain.Audit) error {
	if !validPortalID(tripID) || !validPortalID(annexID) {
		return domain.ErrInvalid
	}
	if approval.CommandID == "" || approval.Actor == "" || approval.Reason == "" || approval.RecordedAt.IsZero() {
		return domain.ErrInvalid
	}
	if root, err := s.read(ctx, "TRIP#"+tripID, "ANNEX#"+annexID); err == nil && root.Status == "APPLIED" {
		if root.Approval != nil && *root.Approval == approval {
			return nil
		}
		return ErrReplayMismatch
	}
	if err := s.BeginAnnexApprovalWithAudit(ctx, tripID, annexID, approval); err != nil {
		return fmt.Errorf("iniciar aprobacion de anexo: %w", err)
	}
	rows, err := s.allAnnexProposals(ctx, tripID, annexID)
	if err != nil {
		return err
	}
	// Un reingreso conserva temporalmente el lookup en la participación saliente.
	// Aplicar todas las bajas primero permite moverlo después de forma condicional.
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Change.Event.Type == "PARTICIPANT_WITHDRAWN" && rows[j].Change.Event.Type == "PARTICIPANT_ADMITTED"
	})
	for _, row := range rows {
		if err := s.applyAnnexProposal(ctx, tripID, annexID, row); err != nil {
			return fmt.Errorf("aplicar cuenta %s: %w", row.AccountID, err)
		}
	}
	if err := s.publishAnnex(ctx, tripID, annexID); err != nil {
		return fmt.Errorf("publicar anexo: %w", err)
	}
	return nil
}

func (s Service) applyAnnexProposal(ctx context.Context, tripID, annexID string, proposal record) error {
	effectPK, effectSK := "TRIP#"+tripID, "ANNEX#"+annexID+"#EFFECT#"+proposal.AccountID
	for retry := 0; retry < 6; retry++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if effect, err := s.read(ctx, effectPK, effectSK); err == nil {
			if effect.Fingerprint != proposal.Fingerprint || effect.Change == nil {
				return ErrReplayMismatch
			}
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		root, err := s.read(ctx, effectPK, "ANNEX#"+annexID)
		if err != nil {
			return err
		}
		if root.Status != "APPLYING" || root.Applied >= root.Expected || root.Fingerprint != proposal.Fingerprint || proposal.Change == nil {
			return domain.ErrConflict
		}
		change := *proposal.Change
		if err := s.validateCurrentAnnexProposal(ctx, tripID, annexID, proposal); err != nil {
			if effect, readErr := s.read(ctx, effectPK, effectSK); readErr == nil && effect.Fingerprint == proposal.Fingerprint {
				return nil
			}
			return err
		}
		rootPrevious := root.Version
		root.Version++
		root.Applied++
		rootWrite, err := s.put(root, "#version = :previous", versionValue(rootPrevious))
		if err != nil {
			return err
		}
		var accountWrite types.TransactWriteItem
		if change.Event.Type == "PARTICIPANT_ADMITTED" {
			accountWrite, err = s.put(record{PK: "ACCOUNT#" + change.Account.ID, SK: "META", Version: change.Account.Version, Account: &change.Account, Fingerprint: proposal.Fingerprint}, "attribute_not_exists(pk)", nil)
		} else {
			accountWrite, err = s.put(record{PK: "ACCOUNT#" + change.Account.ID, SK: "META", Version: change.Account.Version, Account: &change.Account, Fingerprint: proposal.Fingerprint}, "#version = :previous", versionValue(change.Account.Version-1))
		}
		if err != nil {
			return err
		}
		eventWrite, err := s.put(record{PK: "ACCOUNT#" + change.Account.ID, SK: "EVENT#" + fmt.Sprintf("%020d", change.Account.Version), Event: &change.Event}, "attribute_not_exists(pk)", nil)
		if err != nil {
			return err
		}
		effectWrite, err := s.put(record{PK: effectPK, SK: effectSK, Fingerprint: proposal.Fingerprint, AccountID: proposal.AccountID, Change: &change, Status: "APPLIED"}, "attribute_not_exists(pk)", nil)
		if err != nil {
			return err
		}
		if proposal.Roster == nil {
			return domain.ErrInvalid
		}
		roster := *proposal.Roster
		roster.Active, roster.Free, roster.Version = change.Account.Active, change.Account.Free, change.Account.Version
		var rosterWrite types.TransactWriteItem
		if change.Event.Type == "PARTICIPANT_ADMITTED" {
			rosterWrite, err = s.put(record{PK: effectPK, SK: "MEMBER#" + change.Account.ID, AccountID: change.Account.ID, TripID: tripID, Version: change.Account.Version, Roster: &roster, Fingerprint: proposal.Fingerprint}, "attribute_not_exists(pk)", nil)
		} else {
			rosterWrite, err = s.put(record{PK: effectPK, SK: "MEMBER#" + change.Account.ID, AccountID: change.Account.ID, TripID: tripID, Version: change.Account.Version, Roster: &roster, Fingerprint: proposal.Fingerprint}, "#version = :previous", versionValue(change.Account.Version-1))
		}
		if err != nil {
			return err
		}
		writes := []types.TransactWriteItem{s.updatingAnnexCheck(tripID, annexID), rootWrite, accountWrite, rosterWrite, eventWrite, effectWrite}
		if change.Event.Type == "PARTICIPANT_ADMITTED" && proposal.LookupKey != "" {
			condition := "attribute_not_exists(pk)"
			var values map[string]types.AttributeValue
			if proposal.RelatedAccountID != "" {
				condition = "attribute_not_exists(pk) OR accountId = :previous"
				values = map[string]types.AttributeValue{":previous": &types.AttributeValueMemberS{Value: proposal.RelatedAccountID}}
			}
			lookupWrite, buildErr := s.put(record{PK: effectPK, SK: proposal.LookupKey, AccountID: change.Account.ID, TripID: tripID, Fingerprint: proposal.Fingerprint}, condition, values)
			if buildErr != nil {
				return buildErr
			}
			writes = append(writes, lookupWrite)
			adminLookupWrite, buildErr := s.put(record{PK: "ADMIN_" + proposal.LookupKey, SK: "ACCOUNT#" + change.Account.ID, AccountID: change.Account.ID, TripID: tripID, PassengerName: roster.Name, Fingerprint: proposal.Fingerprint}, "attribute_not_exists(pk)", nil)
			if buildErr != nil {
				return buildErr
			}
			writes = append(writes, adminLookupWrite)
		}
		_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
		if err == nil {
			return nil
		}
		if effect, readErr := s.read(ctx, effectPK, effectSK); readErr == nil && effect.Fingerprint == proposal.Fingerprint {
			return nil
		}
		var cancelled *types.TransactionCanceledException
		if !errors.As(err, &cancelled) {
			return fmt.Errorf("aplicar efecto de anexo: %w", err)
		}
	}
	return domain.ErrConflict
}

func (s Service) publishAnnex(ctx context.Context, tripID, annexID string) error {
	for retry := 0; retry < 6; retry++ {
		plan, err := s.read(ctx, "TRIP#"+tripID, "META")
		if err != nil {
			return err
		}
		root, err := s.read(ctx, "TRIP#"+tripID, "ANNEX#"+annexID)
		if err != nil {
			return err
		}
		if root.Status == "APPLIED" {
			latestPlan, readErr := s.read(ctx, plan.PK, plan.SK)
			if readErr == nil && latestPlan.Status == "ACTIVE" && latestPlan.PendingAnnexID == "" {
				return nil
			}
			continue
		}
		if root.Status != "APPLYING" || root.Applied != root.Expected || plan.Status != "UPDATING_ROSTER" || plan.PendingAnnexID != annexID {
			return domain.ErrConflict
		}
		rootPrevious, planPrevious := root.Version, plan.Version
		root.Version++
		root.Status = "APPLIED"
		plan.Version++
		plan.Status = "ACTIVE"
		plan.PendingAnnexID = ""
		rootWrite, err := s.put(root, "#version = :previous", versionValue(rootPrevious))
		if err != nil {
			return err
		}
		planWrite, err := s.put(plan, "#version = :previous", versionValue(planPrevious))
		if err != nil {
			return err
		}
		_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{rootWrite, planWrite}})
		if err == nil {
			return nil
		}
	}
	return domain.ErrConflict
}
