package collection

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// BeginAnnexApproval bloquea la gira y prevalida el borrador completo. No aplica
// propuestas: al terminar deja ANNEX=APPLYING y TRIP=UPDATING_ROSTER para la fase siguiente.
func (s Service) BeginAnnexApproval(ctx context.Context, tripID, annexID string) error {
	root, err := s.read(ctx, "TRIP#"+tripID, "ANNEX#"+annexID)
	if err != nil || root.Event == nil {
		return domain.ErrInvalid
	}
	return s.BeginAnnexApprovalWithAudit(ctx, tripID, annexID, root.Event.Audit)
}

// BeginAnnexApprovalWithAudit registra una aprobación distinta de la creación
// del borrador y hace que su replay sea verificable.
func (s Service) BeginAnnexApprovalWithAudit(ctx context.Context, tripID, annexID string, approval domain.Audit) error {
	if !validPortalID(tripID) || !validPortalID(annexID) || s.DB == nil || s.Table == "" {
		return domain.ErrInvalid
	}
	if approval.CommandID == "" || approval.Actor == "" || approval.Reason == "" || approval.RecordedAt.IsZero() {
		return domain.ErrInvalid
	}
	if err := s.lockAnnexApproval(ctx, tripID, annexID, approval); err != nil {
		return err
	}
	lockedRoot, err := s.read(ctx, "TRIP#"+tripID, "ANNEX#"+annexID)
	if err != nil {
		return err
	}
	if lockedRoot.Status == "APPLYING" || lockedRoot.Status == "APPLIED" {
		return nil
	}
	rows, err := s.allAnnexProposals(ctx, tripID, annexID)
	if err != nil {
		return err
	}
	root, err := s.read(ctx, "TRIP#"+tripID, "ANNEX#"+annexID)
	if err != nil {
		return err
	}
	if root.Status == "APPLYING" {
		return nil
	}
	if int64(len(rows)) != root.Expected || root.Status != "VALIDATING" {
		return domain.ErrConflict
	}
	for _, row := range rows {
		if err := s.validateCurrentAnnexProposal(ctx, tripID, annexID, row); err != nil {
			latest, readErr := s.read(ctx, "TRIP#"+tripID, "ANNEX#"+annexID)
			if readErr == nil && (latest.Status == "APPLYING" || latest.Status == "APPLIED") {
				return nil
			}
			if rejectErr := s.finishAnnexValidation(ctx, tripID, annexID, "REJECTED"); rejectErr != nil {
				return rejectErr
			}
			return domain.ErrConflict
		}
	}
	return s.finishAnnexValidation(ctx, tripID, annexID, "APPLYING")
}

func (s Service) lockAnnexApproval(ctx context.Context, tripID, annexID string, approval domain.Audit) error {
	plan, err := s.read(ctx, "TRIP#"+tripID, "META")
	if err != nil {
		return err
	}
	root, err := s.read(ctx, "TRIP#"+tripID, "ANNEX#"+annexID)
	if err != nil {
		return err
	}
	if plan.Status == "ACTIVE" && plan.PendingAnnexID == "" && root.Status == "APPLIED" {
		if root.Approval != nil && *root.Approval == approval {
			return nil
		}
		return ErrReplayMismatch
	}
	if plan.Status == "UPDATING_ROSTER" && plan.PendingAnnexID == annexID && (root.Status == "VALIDATING" || root.Status == "APPLYING") {
		if root.Approval != nil && *root.Approval == approval {
			return nil
		}
		return ErrReplayMismatch
	}
	if plan.Status != "ACTIVE" || plan.RosterClosed || plan.PendingAnnexID != "" || root.Status != "DRAFT" || root.Prepared != root.Expected || root.Expected < 1 {
		return domain.ErrConflict
	}
	planPrevious, rootPrevious := plan.Version, root.Version
	plan.Version++
	plan.Status = "UPDATING_ROSTER"
	plan.PendingAnnexID = annexID
	root.Version++
	root.Status = "VALIDATING"
	root.Approval = &approval
	planWrite, err := s.put(plan, "#version = :previous", versionValue(planPrevious))
	if err != nil {
		return err
	}
	rootWrite, err := s.put(root, "#version = :previous", versionValue(rootPrevious))
	if err != nil {
		return err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{planWrite, rootWrite}})
	if err == nil {
		return nil
	}
	latestPlan, planErr := s.read(ctx, plan.PK, plan.SK)
	latestRoot, rootErr := s.read(ctx, root.PK, root.SK)
	if planErr == nil && rootErr == nil {
		if latestPlan.Status == "UPDATING_ROSTER" && latestPlan.PendingAnnexID == annexID && (latestRoot.Status == "VALIDATING" || latestRoot.Status == "APPLYING") {
			if latestRoot.Approval != nil && *latestRoot.Approval == approval {
				return nil
			}
			return ErrReplayMismatch
		}
		if latestPlan.Status == "ACTIVE" && latestPlan.PendingAnnexID == "" && latestRoot.Status == "APPLIED" {
			if latestRoot.Approval != nil && *latestRoot.Approval == approval {
				return nil
			}
			return ErrReplayMismatch
		}
	}
	return domain.ErrConflict
}

func (s Service) allAnnexProposals(ctx context.Context, tripID, annexID string) ([]record, error) {
	db, ok := s.DB.(queryDatabase)
	if !ok {
		return nil, domain.ErrInvalid
	}
	pk, prefix := "TRIP#"+tripID, "ANNEX#"+annexID+"#PROPOSAL#"
	var cursor map[string]types.AttributeValue
	rows := []record{}
	for {
		out, err := db.Query(ctx, &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}, ":prefix": &types.AttributeValueMemberS{Value: prefix}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(20), ExclusiveStartKey: cursor})
		if err != nil {
			return nil, err
		}
		for _, item := range out.Items {
			var row record
			if attributevalue.UnmarshalMap(item, &row) != nil || row.PK != pk || row.SK != prefix+row.AccountID || row.Change == nil || !validPortalID(row.AccountID) {
				return nil, domain.ErrInvalid
			}
			rows = append(rows, row)
			if len(rows) > 1000 {
				return nil, domain.ErrInvalid
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		sk, ok := out.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS)
		if !ok || !strings.HasPrefix(sk.Value, prefix) {
			return nil, domain.ErrInvalid
		}
		cursor = out.LastEvaluatedKey
	}
	return rows, nil
}

func (s Service) validateCurrentAnnexProposal(ctx context.Context, tripID, annexID string, row record) error {
	c := row.Change
	if c.Account.TripID != tripID || c.Event.Reference != annexID || row.Fingerprint == "" {
		return domain.ErrInvalid
	}
	current, err := s.read(ctx, "ACCOUNT#"+row.AccountID, "META")
	switch c.Event.Type {
	case "PARTICIPANT_ADMITTED":
		if !errors.Is(err, ErrNotFound) || c.Account.Version != 1 || !c.Account.Active {
			return domain.ErrConflict
		}
		if row.LookupKey != "" {
			lookup, lookupErr := s.read(ctx, "TRIP#"+tripID, row.LookupKey)
			if lookupErr == nil {
				if row.RelatedAccountID == "" || lookup.AccountID != row.RelatedAccountID {
					return domain.ErrConflict
				}
			} else if !errors.Is(lookupErr, ErrNotFound) {
				return lookupErr
			}
		}
	case "PARTICIPANT_WITHDRAWN":
		if err != nil || current.Account == nil || current.Account.TripID != tripID || current.Account.Version != c.Account.Version-1 || current.Version != current.Account.Version || !current.Account.Active {
			return domain.ErrConflict
		}
	default:
		return domain.ErrInvalid
	}
	return nil
}

func (s Service) finishAnnexValidation(ctx context.Context, tripID, annexID, status string) error {
	plan, err := s.read(ctx, "TRIP#"+tripID, "META")
	if err != nil {
		return err
	}
	root, err := s.read(ctx, "TRIP#"+tripID, "ANNEX#"+annexID)
	if err != nil {
		return err
	}
	if status == "APPLYING" && ((plan.Status == "UPDATING_ROSTER" && plan.PendingAnnexID == annexID && root.Status == "APPLYING") || (plan.Status == "ACTIVE" && plan.PendingAnnexID == "" && root.Status == "APPLIED")) {
		return nil
	}
	if status == "REJECTED" && plan.Status == "ACTIVE" && plan.PendingAnnexID == "" && root.Status == "REJECTED" {
		return nil
	}
	if plan.Status != "UPDATING_ROSTER" || plan.PendingAnnexID != annexID || root.Status != "VALIDATING" || (status != "APPLYING" && status != "REJECTED") {
		return domain.ErrConflict
	}
	planPrevious, rootPrevious := plan.Version, root.Version
	root.Version++
	root.Status = status
	rootWrite, err := s.put(root, "#version = :previous", versionValue(rootPrevious))
	if err != nil {
		return err
	}
	writes := []types.TransactWriteItem{rootWrite}
	if status == "REJECTED" {
		plan.Version++
		plan.Status = "ACTIVE"
		plan.PendingAnnexID = ""
		planWrite, buildErr := s.put(plan, "#version = :previous", versionValue(planPrevious))
		if buildErr != nil {
			return buildErr
		}
		writes = append(writes, planWrite)
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err != nil {
		latestPlan, planErr := s.read(ctx, plan.PK, plan.SK)
		latestRoot, rootErr := s.read(ctx, root.PK, root.SK)
		if planErr == nil && rootErr == nil {
			if status == "APPLYING" && latestPlan.Status == "UPDATING_ROSTER" && latestPlan.PendingAnnexID == annexID && latestRoot.Status == "APPLYING" {
				return nil
			}
			if status == "REJECTED" && latestPlan.Status == "ACTIVE" && latestPlan.PendingAnnexID == "" && latestRoot.Status == "REJECTED" {
				return nil
			}
		}
		return domain.ErrConflict
	}
	return nil
}
