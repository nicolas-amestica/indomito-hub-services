package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type GroupDiscountInput struct {
	ID            string
	TripID        string
	AccountIDs    []string
	BasisPoints   int64
	CreationAudit domain.Audit
}

type GroupDiscountState struct {
	ID          string `json:"id"`
	TripID      string `json:"tripId"`
	Status      string `json:"status"`
	BasisPoints int64  `json:"basisPoints"`
	Expected    int64  `json:"expected"`
	Prepared    int64  `json:"prepared"`
	Applied     int64  `json:"applied"`
	Amount      int64  `json:"amount"`
}

func (s Service) CreateGroupDiscount(ctx context.Context, input GroupDiscountInput) (GroupDiscountState, error) {
	if !validPortalID(input.ID) || !validPortalID(input.TripID) || input.BasisPoints <= 0 || input.BasisPoints > 10000 || input.CreationAudit.CommandID != input.ID || input.CreationAudit.Actor == "" || input.CreationAudit.Reason == "" || input.CreationAudit.RecordedAt.IsZero() {
		return GroupDiscountState{}, domain.ErrInvalid
	}
	ids := append([]string(nil), input.AccountIDs...)
	sort.Strings(ids)
	for index, id := range ids {
		if !validPortalID(id) || index > 0 && id == ids[index-1] {
			return GroupDiscountState{}, domain.ErrInvalid
		}
	}
	input.AccountIDs = ids
	fingerprint, err := groupDiscountFingerprint(input)
	if err != nil {
		return GroupDiscountState{}, err
	}
	pk, sk := "TRIP#"+input.TripID, "GROUP_DISCOUNT#"+input.ID
	if existing, readErr := s.read(ctx, pk, sk); readErr == nil {
		if existing.Fingerprint != fingerprint {
			return GroupDiscountState{}, ErrReplayMismatch
		}
		if existing.Status != "PREPARING" {
			return groupDiscountState(existing), nil
		}
	} else if !errors.Is(readErr, ErrNotFound) {
		return GroupDiscountState{}, readErr
	} else {
		plan, planErr := s.read(ctx, pk, "META")
		if planErr != nil || plan.Status != "ACTIVE" || plan.PendingAnnexID != "" || plan.PendingGroupID != "" || plan.RosterSchemaVersion != 1 {
			return GroupDiscountState{}, domain.ErrConflict
		}
		root := record{PK: pk, SK: sk, TripID: input.TripID, Status: "PREPARING", Version: 1, Fingerprint: fingerprint, BasisPoints: input.BasisPoints, Event: &domain.Event{Audit: input.CreationAudit, SchemaVersion: 1, Type: "GROUP_DISCOUNT_DRAFTED", TripID: input.TripID, BasisPoints: input.BasisPoints}}
		previousPlan := plan.Version
		plan.Version++
		plan.Status = "PREPARING_GROUP_DISCOUNT"
		plan.PendingGroupID = input.ID
		planWrite, buildErr := s.put(plan, "#version = :previous", versionValue(previousPlan))
		if buildErr != nil {
			return GroupDiscountState{}, buildErr
		}
		write, buildErr := s.put(root, "attribute_not_exists(pk)", nil)
		if buildErr != nil {
			return GroupDiscountState{}, buildErr
		}
		if _, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{planWrite, write}}); err != nil {
			return GroupDiscountState{}, domain.ErrConflict
		}
	}
	changes, err := s.groupDiscountChanges(ctx, input)
	if err != nil {
		_ = s.abortPreparingGroupDiscount(ctx, input.TripID, input.ID)
		return GroupDiscountState{}, err
	}
	root, err := s.read(ctx, pk, sk)
	if err != nil {
		return GroupDiscountState{}, err
	}
	if root.Expected == 0 {
		previous := root.Version
		root.Version++
		root.Expected = int64(len(changes))
		write, buildErr := s.put(root, "#version = :previous", versionValue(previous))
		if buildErr != nil {
			return GroupDiscountState{}, buildErr
		}
		if _, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.preparingGroupDiscountCheck(input.TripID, input.ID), write}}); err != nil {
			return GroupDiscountState{}, domain.ErrConflict
		}
	} else if root.Expected != int64(len(changes)) {
		return GroupDiscountState{}, ErrReplayMismatch
	}
	for _, change := range changes {
		proposal := record{PK: pk, SK: sk + "#PROPOSAL#" + change.Account.ID, TripID: input.TripID, AccountID: change.Account.ID, Fingerprint: fingerprint, Change: &change}
		if err = s.prepareGroupDiscountProposal(ctx, input.ID, proposal); err != nil {
			return GroupDiscountState{}, err
		}
	}
	root, err = s.read(ctx, pk, sk)
	if err != nil {
		return GroupDiscountState{}, err
	}
	if root.Status == "PREPARING" && root.Prepared == root.Expected {
		plan, planErr := s.read(ctx, pk, "META")
		if planErr != nil {
			return GroupDiscountState{}, planErr
		}
		previousPlan, previous := plan.Version, root.Version
		plan.Version++
		plan.Status = "ACTIVE"
		plan.PendingGroupID = ""
		root.Version++
		root.Status = "DRAFT"
		planWrite, buildErr := s.put(plan, "#version = :previous", versionValue(previousPlan))
		if buildErr != nil {
			return GroupDiscountState{}, buildErr
		}
		write, buildErr := s.put(root, "#version = :previous", versionValue(previous))
		if buildErr != nil {
			return GroupDiscountState{}, buildErr
		}
		if _, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.preparingGroupDiscountCheck(input.TripID, input.ID), planWrite, write}}); err != nil {
			return GroupDiscountState{}, domain.ErrConflict
		}
	}
	root, err = s.read(ctx, pk, sk)
	return groupDiscountState(root), err
}

func (s Service) abortPreparingGroupDiscount(ctx context.Context, tripID, id string) error {
	plan, err := s.read(ctx, "TRIP#"+tripID, "META")
	if err != nil {
		return err
	}
	root, err := s.read(ctx, plan.PK, "GROUP_DISCOUNT#"+id)
	if err != nil {
		return err
	}
	if root.Status != "PREPARING" || plan.Status != "PREPARING_GROUP_DISCOUNT" || plan.PendingGroupID != id {
		return nil
	}
	previousPlan, previousRoot := plan.Version, root.Version
	plan.Version++
	plan.Status = "ACTIVE"
	plan.PendingGroupID = ""
	root.Version++
	root.Status = "REJECTED"
	planWrite, err := s.put(plan, "#version = :previous", versionValue(previousPlan))
	if err != nil {
		return err
	}
	rootWrite, err := s.put(root, "#version = :previous", versionValue(previousRoot))
	if err != nil {
		return err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.preparingGroupDiscountCheck(tripID, id), planWrite, rootWrite}})
	return err
}

func groupDiscountFingerprint(input GroupDiscountInput) (string, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func (s Service) groupDiscountChanges(ctx context.Context, input GroupDiscountInput) ([]domain.Change, error) {
	selected := map[string]bool{}
	for _, id := range input.AccountIDs {
		selected[id] = true
	}
	db, ok := s.DB.(queryDatabase)
	if !ok {
		return nil, domain.ErrInvalid
	}
	changes := []domain.Change{}
	var cursor map[string]types.AttributeValue
	for {
		out, err := db.Query(ctx, &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "TRIP#" + input.TripID}, ":prefix": &types.AttributeValueMemberS{Value: "MEMBER#"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(50), ExclusiveStartKey: cursor})
		if err != nil {
			return nil, err
		}
		for _, raw := range out.Items {
			var member record
			if attributevalue.UnmarshalMap(raw, &member) != nil || member.Roster == nil {
				return nil, domain.ErrInvalid
			}
			if len(selected) > 0 && !selected[member.AccountID] {
				continue
			}
			accountRow, readErr := s.read(ctx, "ACCOUNT#"+member.AccountID, "META")
			if readErr != nil || accountRow.Account == nil || accountRow.Account.TripID != input.TripID {
				return nil, domain.ErrConflict
			}
			a := *accountRow.Account
			if !a.Active || a.Free {
				if selected[member.AccountID] {
					return nil, domain.ErrInvalid
				}
				continue
			}
			ids := []string{}
			for _, installment := range a.Installments {
				if installment.Outstanding() > 0 {
					ids = append(ids, installment.ID)
				}
			}
			if len(ids) == 0 {
				if selected[member.AccountID] {
					return nil, domain.ErrInvalid
				}
				continue
			}
			audit := input.CreationAudit
			audit.CommandID = paymentOperationID(input.ID + ":" + a.ID)
			change, changeErr := domain.Discount(a, audit, ids, input.BasisPoints)
			if changeErr != nil {
				return nil, changeErr
			}
			changes = append(changes, change)
			delete(selected, member.AccountID)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		cursor = out.LastEvaluatedKey
	}
	if len(selected) > 0 || len(changes) == 0 {
		return nil, domain.ErrInvalid
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Account.ID < changes[j].Account.ID })
	return changes, nil
}

func (s Service) prepareGroupDiscountProposal(ctx context.Context, id string, proposal record) error {
	if saved, err := s.read(ctx, proposal.PK, proposal.SK); err == nil {
		return nilIfSameFingerprint(saved, proposal.Fingerprint)
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	for retry := 0; retry < 6; retry++ {
		root, err := s.read(ctx, proposal.PK, "GROUP_DISCOUNT#"+id)
		if err != nil {
			return err
		}
		if root.Status != "PREPARING" || root.Prepared >= root.Expected {
			return domain.ErrConflict
		}
		previous := root.Version
		root.Version++
		root.Prepared++
		root.Event.Amount += proposal.Change.Event.Amount
		rw, e := s.put(root, "#version = :previous", versionValue(previous))
		if e != nil {
			return e
		}
		pw, e := s.put(proposal, "attribute_not_exists(pk)", nil)
		if e != nil {
			return e
		}
		_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.preparingGroupDiscountCheck(proposal.TripID, id), rw, pw}})
		if err == nil {
			return nil
		}
		if saved, re := s.read(ctx, proposal.PK, proposal.SK); re == nil && saved.Fingerprint == proposal.Fingerprint {
			return nil
		}
	}
	return domain.ErrConflict
}

func nilIfSameFingerprint(row record, fingerprint string) error {
	if row.Fingerprint != fingerprint || row.Change == nil {
		return ErrReplayMismatch
	}
	return nil
}

func (s Service) ApproveGroupDiscount(ctx context.Context, tripID, id string, approval domain.Audit) (GroupDiscountState, error) {
	if !validPortalID(tripID) || !validPortalID(id) || approval.CommandID == "" || approval.Actor == "" || approval.Reason == "" || approval.RecordedAt.IsZero() {
		return GroupDiscountState{}, domain.ErrInvalid
	}
	pk, sk := "TRIP#"+tripID, "GROUP_DISCOUNT#"+id
	root, err := s.read(ctx, pk, sk)
	if err != nil {
		return GroupDiscountState{}, err
	}
	if root.Status == "APPLIED" {
		return groupDiscountState(root), nil
	}
	if root.Status == "REJECTED" {
		return GroupDiscountState{}, domain.ErrConflict
	}
	if root.Approval != nil && *root.Approval != approval {
		return GroupDiscountState{}, ErrReplayMismatch
	}
	if root.Status == "DRAFT" {
		plan, e := s.read(ctx, pk, "META")
		if e != nil || plan.Status != "ACTIVE" || plan.PendingGroupID != "" {
			return GroupDiscountState{}, domain.ErrConflict
		}
		previousPlan, previousRoot := plan.Version, root.Version
		plan.Version++
		plan.Status = "APPLYING_GROUP_DISCOUNT"
		plan.PendingGroupID = id
		root.Version++
		root.Status = "VALIDATING"
		root.Approval = &approval
		pw, e := s.put(plan, "#version = :previous", versionValue(previousPlan))
		if e != nil {
			return GroupDiscountState{}, e
		}
		rw, e := s.put(root, "#version = :previous", versionValue(previousRoot))
		if e != nil {
			return GroupDiscountState{}, e
		}
		if _, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{pw, rw}}); err != nil {
			return GroupDiscountState{}, domain.ErrConflict
		}
	}
	proposals, err := s.allGroupDiscountProposals(ctx, tripID, id)
	if err != nil {
		return GroupDiscountState{}, err
	}
	root, err = s.read(ctx, pk, sk)
	if err != nil {
		return GroupDiscountState{}, err
	}
	if root.Status == "VALIDATING" {
		valid := int64(len(proposals)) == root.Expected
		for _, p := range proposals {
			current, e := s.read(ctx, "ACCOUNT#"+p.AccountID, "META")
			if e != nil || current.Account == nil || p.Change == nil || current.Account.Version+1 != p.Change.Account.Version || current.Account.OpenAttemptID != "" {
				valid = false
				break
			}
		}
		if !valid {
			return GroupDiscountState{}, s.rejectGroupDiscount(ctx, tripID, id)
		}
		previous := root.Version
		root.Version++
		root.Status = "APPLYING"
		rw, e := s.put(root, "#version = :previous", versionValue(previous))
		if e != nil {
			return GroupDiscountState{}, e
		}
		if _, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.applyingGroupDiscountCheck(tripID, id), rw}}); err != nil {
			return GroupDiscountState{}, domain.ErrConflict
		}
	}
	for _, proposal := range proposals {
		if err = s.applyGroupDiscountProposal(ctx, tripID, id, proposal); err != nil {
			return GroupDiscountState{}, err
		}
	}
	if err = s.publishGroupDiscount(ctx, tripID, id); err != nil {
		return GroupDiscountState{}, err
	}
	root, err = s.read(ctx, pk, sk)
	return groupDiscountState(root), err
}

func (s Service) allGroupDiscountProposals(ctx context.Context, tripID, id string) ([]record, error) {
	db, ok := s.DB.(queryDatabase)
	if !ok {
		return nil, domain.ErrInvalid
	}
	rows := []record{}
	var cursor map[string]types.AttributeValue
	for {
		out, err := db.Query(ctx, &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "TRIP#" + tripID}, ":prefix": &types.AttributeValueMemberS{Value: "GROUP_DISCOUNT#" + id + "#PROPOSAL#"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(50), ExclusiveStartKey: cursor})
		if err != nil {
			return nil, err
		}
		for _, raw := range out.Items {
			var row record
			if attributevalue.UnmarshalMap(raw, &row) != nil || row.Change == nil {
				return nil, domain.ErrInvalid
			}
			rows = append(rows, row)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		cursor = out.LastEvaluatedKey
	}
	return rows, nil
}

func (s Service) rejectGroupDiscount(ctx context.Context, tripID, id string) error {
	plan, e := s.read(ctx, "TRIP#"+tripID, "META")
	if e != nil {
		return e
	}
	root, e := s.read(ctx, plan.PK, "GROUP_DISCOUNT#"+id)
	if e != nil {
		return e
	}
	pp, pr := plan.Version, root.Version
	plan.Version++
	plan.Status = "ACTIVE"
	plan.PendingGroupID = ""
	root.Version++
	root.Status = "REJECTED"
	pw, e := s.put(plan, "#version = :previous", versionValue(pp))
	if e != nil {
		return e
	}
	rw, e := s.put(root, "#version = :previous", versionValue(pr))
	if e != nil {
		return e
	}
	_, e = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.applyingGroupDiscountCheck(tripID, id), pw, rw}})
	if e != nil {
		return domain.ErrConflict
	}
	return domain.ErrConflict
}

func (s Service) applyGroupDiscountProposal(ctx context.Context, tripID, id string, p record) error {
	effectSK := "GROUP_DISCOUNT#" + id + "#EFFECT#" + p.AccountID
	if _, e := s.read(ctx, "ACCOUNT#"+p.AccountID, effectSK); e == nil {
		return nil
	} else if !errors.Is(e, ErrNotFound) {
		return e
	}
	for retry := 0; retry < 6; retry++ {
		root, e := s.read(ctx, "TRIP#"+tripID, "GROUP_DISCOUNT#"+id)
		if e != nil {
			return e
		}
		current, e := s.read(ctx, "ACCOUNT#"+p.AccountID, "META")
		if e != nil || current.Account == nil || p.Change == nil {
			return domain.ErrConflict
		}
		if current.Account.Version+1 != p.Change.Account.Version {
			return domain.ErrConflict
		}
		previous := root.Version
		root.Version++
		root.Applied++
		accountWrite, e := s.put(record{PK: "ACCOUNT#" + p.AccountID, SK: "META", Version: p.Change.Account.Version, Account: &p.Change.Account}, "#version = :previous", versionValue(current.Account.Version))
		if e != nil {
			return e
		}
		eventWrite, e := s.put(record{PK: "ACCOUNT#" + p.AccountID, SK: "EVENT#" + p.Change.Event.RecordedAt.UTC().Format("20060102T150405.000000000") + "#" + p.Change.Event.CommandID, Event: &p.Change.Event}, "attribute_not_exists(pk)", nil)
		if e != nil {
			return e
		}
		effectWrite, e := s.put(record{PK: "ACCOUNT#" + p.AccountID, SK: effectSK, Fingerprint: p.Fingerprint}, "attribute_not_exists(pk)", nil)
		if e != nil {
			return e
		}
		rootWrite, e := s.put(root, "#version = :previous", versionValue(previous))
		if e != nil {
			return e
		}
		member, e := s.read(ctx, "TRIP#"+tripID, "MEMBER#"+p.AccountID)
		if e != nil || member.Roster == nil {
			return domain.ErrConflict
		}
		roster := *member.Roster
		roster.Version = p.Change.Account.Version
		memberWrite, e := s.put(record{PK: member.PK, SK: member.SK, TripID: tripID, AccountID: p.AccountID, Version: p.Change.Account.Version, Roster: &roster}, "#version = :previous", versionValue(member.Version))
		if e != nil {
			return e
		}
		_, e = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.applyingGroupDiscountCheck(tripID, id), accountWrite, memberWrite, eventWrite, effectWrite, rootWrite}})
		if e == nil {
			return nil
		}
		if _, re := s.read(ctx, "ACCOUNT#"+p.AccountID, effectSK); re == nil {
			return nil
		}
	}
	return domain.ErrConflict
}

func (s Service) publishGroupDiscount(ctx context.Context, tripID, id string) error {
	for retry := 0; retry < 6; retry++ {
		plan, e := s.read(ctx, "TRIP#"+tripID, "META")
		if e != nil {
			return e
		}
		root, e := s.read(ctx, plan.PK, "GROUP_DISCOUNT#"+id)
		if e != nil {
			return e
		}
		if root.Status == "APPLIED" && plan.Status == "ACTIVE" {
			return nil
		}
		if root.Status != "APPLYING" || root.Applied != root.Expected {
			return domain.ErrConflict
		}
		pp, pr := plan.Version, root.Version
		plan.Version++
		plan.Status = "ACTIVE"
		plan.PendingGroupID = ""
		root.Version++
		root.Status = "APPLIED"
		pw, e := s.put(plan, "#version = :previous", versionValue(pp))
		if e != nil {
			return e
		}
		rw, e := s.put(root, "#version = :previous", versionValue(pr))
		if e != nil {
			return e
		}
		_, e = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.applyingGroupDiscountCheck(tripID, id), pw, rw}})
		if e == nil {
			return nil
		}
	}
	return domain.ErrConflict
}

func groupDiscountState(root record) GroupDiscountState {
	amount := int64(0)
	if root.Event != nil {
		amount = root.Event.Amount
	}
	return GroupDiscountState{ID: root.SK[len("GROUP_DISCOUNT#"):], TripID: root.TripID, Status: root.Status, BasisPoints: root.BasisPoints, Expected: root.Expected, Prepared: root.Prepared, Applied: root.Applied, Amount: amount}
}
