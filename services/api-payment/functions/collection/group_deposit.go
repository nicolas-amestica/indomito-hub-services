package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type GroupDepositInput struct {
	ID            string       `json:"id"`
	TripID        string       `json:"tripId"`
	Amount        int64        `json:"amount"`
	Reference     string       `json:"reference"`
	EffectiveDate string       `json:"effectiveDate"`
	ReceiptEmail  string       `json:"receiptEmail"`
	Audit         domain.Audit `json:"audit"`
}

type GroupDepositState struct {
	ID       string `json:"id"`
	TripID   string `json:"tripId"`
	Status   string `json:"status"`
	Amount   int64  `json:"amount"`
	Prepared int64  `json:"prepared"`
	Applied  int64  `json:"applied"`
	Expected int64  `json:"expected"`
}

func (s Service) ApplyGroupDeposit(ctx context.Context, input GroupDepositInput) (GroupDepositState, error) {
	address, emailErr := mail.ParseAddress(input.ReceiptEmail)
	if !validPortalID(input.ID) || !validPortalID(input.TripID) || input.Audit.CommandID != input.ID || input.Amount <= 0 || input.Reference == "" || input.EffectiveDate == "" || input.Audit.Actor == "" || input.Audit.Reason == "" || input.Audit.RecordedAt.IsZero() || emailErr != nil || address.Address != input.ReceiptEmail || len(input.ReceiptEmail) > 254 || s.DB == nil || s.Table == "" {
		return GroupDepositState{}, domain.ErrInvalid
	}
	basicHash, err := groupDepositFingerprint(input, nil)
	if err != nil {
		return GroupDepositState{}, err
	}
	root, err := s.beginGroupDeposit(ctx, input, basicHash)
	if err != nil {
		return GroupDepositState{}, err
	}
	if root.Status == "APPLIED" {
		return groupDepositState(root), nil
	}
	if root.Status == "PREPARING" {
		if err = s.prepareGroupDeposit(ctx, input, root, basicHash); err != nil {
			return GroupDepositState{}, err
		}
	}
	root, err = s.read(ctx, "TRIP#"+input.TripID, "GROUP_DEPOSIT#"+input.ID)
	if err != nil {
		return GroupDepositState{}, err
	}
	if root.Status == "APPLYING" {
		rows, readErr := s.allGroupDepositAllocations(ctx, input.TripID, input.ID)
		if readErr != nil {
			return GroupDepositState{}, readErr
		}
		for _, row := range rows {
			if err = s.applyGroupDepositAllocation(ctx, input.TripID, input.ID, row); err != nil {
				return GroupDepositState{}, err
			}
		}
		if err = s.publishGroupDeposit(ctx, input.TripID, input.ID); err != nil {
			return GroupDepositState{}, err
		}
	}
	root, err = s.read(ctx, "TRIP#"+input.TripID, "GROUP_DEPOSIT#"+input.ID)
	if err != nil {
		return GroupDepositState{}, err
	}
	return groupDepositState(root), nil
}

func groupDepositFingerprint(input GroupDepositInput, allocations []domain.DepositAllocation) (string, error) {
	payload := struct {
		ID            string                     `json:"id"`
		TripID        string                     `json:"tripId"`
		Amount        int64                      `json:"amount"`
		Reference     string                     `json:"reference"`
		EffectiveDate string                     `json:"effectiveDate"`
		ReceiptEmail  string                     `json:"receiptEmail"`
		Audit         domain.Audit               `json:"audit"`
		Allocations   []domain.DepositAllocation `json:"allocations,omitempty"`
	}{input.ID, input.TripID, input.Amount, input.Reference, input.EffectiveDate, input.ReceiptEmail, input.Audit, allocations}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func (s Service) beginGroupDeposit(ctx context.Context, input GroupDepositInput, fingerprint string) (record, error) {
	pk, sk := "TRIP#"+input.TripID, "GROUP_DEPOSIT#"+input.ID
	if root, err := s.read(ctx, pk, sk); err == nil {
		if root.Event == nil || root.Event.CommandID != input.ID || root.Event.Amount != input.Amount || root.Event.Reference != input.Reference || root.Event.EffectiveDate != input.EffectiveDate || root.ReceiptEmail != input.ReceiptEmail || root.Event.Audit != input.Audit {
			return record{}, ErrReplayMismatch
		}
		return root, nil
	} else if !errors.Is(err, ErrNotFound) {
		return record{}, err
	}
	plan, err := s.read(ctx, pk, "META")
	if err != nil {
		return record{}, err
	}
	if plan.Status != "ACTIVE" || plan.PendingAnnexID != "" || plan.PendingGroupID != "" || plan.RosterSchemaVersion != 1 {
		return record{}, domain.ErrConflict
	}
	root := record{PK: pk, SK: sk, TripID: input.TripID, Version: 1, Status: "PREPARING", Fingerprint: fingerprint, ReceiptEmail: input.ReceiptEmail, Event: &domain.Event{Audit: input.Audit, SchemaVersion: 1, Type: "GROUP_DEPOSIT_RECEIVED", TripID: input.TripID, Amount: input.Amount, Reference: input.Reference, EffectiveDate: input.EffectiveDate}}
	previous := plan.Version
	plan.Version++
	plan.Status = "APPLYING_GROUP_DEPOSIT"
	plan.PendingGroupID = input.ID
	planWrite, err := s.put(plan, "#version = :previous", versionValue(previous))
	if err != nil {
		return record{}, err
	}
	rootWrite, err := s.put(root, "attribute_not_exists(pk)", nil)
	if err != nil {
		return record{}, err
	}
	referenceWrite, err := s.put(record{PK: "REFERENCE#" + input.Reference, SK: "META", Fingerprint: fingerprint, TripID: input.TripID}, "attribute_not_exists(pk)", nil)
	if err != nil {
		return record{}, err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{planWrite, rootWrite, referenceWrite}})
	if err == nil {
		return root, nil
	}
	if stored, readErr := s.read(ctx, pk, sk); readErr == nil {
		if stored.Event != nil && stored.Event.CommandID == input.ID && stored.Event.Reference == input.Reference {
			return stored, nil
		}
		return record{}, ErrReplayMismatch
	}
	if _, readErr := s.read(ctx, "REFERENCE#"+input.Reference, "META"); readErr == nil {
		return record{}, ErrReferenceUsed
	}
	return record{}, domain.ErrConflict
}

func (s Service) prepareGroupDeposit(ctx context.Context, input GroupDepositInput, root record, basicFingerprint string) error {
	allocations, changes, err := s.groupDepositChanges(ctx, input)
	if err != nil {
		return err
	}
	fullFingerprint, err := groupDepositFingerprint(input, allocations)
	if err != nil {
		return err
	}
	if root.Expected == 0 {
		previous := root.Version
		root.Version++
		root.Expected = int64(len(changes))
		root.Fingerprint = fullFingerprint
		write, buildErr := s.put(root, "#version = :previous", versionValue(previous))
		if buildErr != nil {
			return buildErr
		}
		_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.applyingGroupCheck(input.TripID, input.ID), write}})
		if err != nil {
			return domain.ErrConflict
		}
	} else if root.Fingerprint != fullFingerprint && root.Fingerprint != basicFingerprint {
		return ErrReplayMismatch
	}
	for index, change := range changes {
		allocation := allocations[index]
		proposal := record{PK: root.PK, SK: root.SK + "#ALLOCATION#" + allocation.AccountID, TripID: input.TripID, AccountID: allocation.AccountID, Fingerprint: fullFingerprint, Change: &change}
		if err = s.prepareGroupDepositAllocation(ctx, input.ID, proposal); err != nil {
			return err
		}
	}
	return s.finishGroupDepositPreparation(ctx, input.TripID, input.ID)
}

func (s Service) groupDepositChanges(ctx context.Context, input GroupDepositInput) ([]domain.DepositAllocation, []domain.Change, error) {
	db, ok := s.DB.(queryDatabase)
	if !ok {
		return nil, nil, domain.ErrInvalid
	}
	pk, prefix := "TRIP#"+input.TripID, "MEMBER#"
	var cursor map[string]types.AttributeValue
	accounts := []domain.Account{}
	candidates := []domain.DepositCandidate{}
	for {
		out, err := db.Query(ctx, &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}, ":prefix": &types.AttributeValueMemberS{Value: prefix}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(50), ExclusiveStartKey: cursor})
		if err != nil {
			return nil, nil, err
		}
		for _, raw := range out.Items {
			var member record
			if attributevalue.UnmarshalMap(raw, &member) != nil || member.Roster == nil {
				return nil, nil, domain.ErrInvalid
			}
			accountRow, readErr := s.read(ctx, "ACCOUNT#"+member.AccountID, "META")
			if readErr != nil || accountRow.Account == nil || accountRow.Account.TripID != input.TripID {
				return nil, nil, domain.ErrConflict
			}
			if !accountRow.Account.Active || accountRow.Account.Free {
				continue
			}
			outstanding := accountRow.Account.DepositAgreed - accountRow.Account.DepositReceived
			if outstanding > 0 {
				accounts = append(accounts, *accountRow.Account)
				candidates = append(candidates, domain.DepositCandidate{AccountID: member.AccountID, Outstanding: outstanding})
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		cursor = out.LastEvaluatedKey
	}
	allocations, err := domain.AllocateGroupDeposit(input.Amount, candidates)
	if err != nil {
		return nil, nil, err
	}
	byID := map[string]domain.Account{}
	for _, account := range accounts {
		byID[account.ID] = account
	}
	changes := make([]domain.Change, 0, len(allocations))
	for _, allocation := range allocations {
		audit := input.Audit
		audit.CommandID = paymentOperationID(input.ID + ":" + allocation.AccountID)
		change, changeErr := domain.RecordDeposit(byID[allocation.AccountID], audit, allocation.Amount, input.Reference, input.EffectiveDate)
		if changeErr != nil {
			return nil, nil, changeErr
		}
		changes = append(changes, change)
	}
	return allocations, changes, nil
}

func (s Service) prepareGroupDepositAllocation(ctx context.Context, commandID string, proposal record) error {
	if stored, err := s.read(ctx, proposal.PK, proposal.SK); err == nil {
		if stored.Fingerprint != proposal.Fingerprint || stored.Change == nil {
			return ErrReplayMismatch
		}
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	for retry := 0; retry < 6; retry++ {
		root, err := s.read(ctx, proposal.PK, "GROUP_DEPOSIT#"+commandID)
		if err != nil {
			return err
		}
		if root.Status != "PREPARING" || root.Prepared >= root.Expected || root.Fingerprint != proposal.Fingerprint {
			return domain.ErrConflict
		}
		previous := root.Version
		root.Version++
		root.Prepared++
		rootWrite, err := s.put(root, "#version = :previous", versionValue(previous))
		if err != nil {
			return err
		}
		proposalWrite, err := s.put(proposal, "attribute_not_exists(pk)", nil)
		if err != nil {
			return err
		}
		_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.applyingGroupCheck(proposal.TripID, commandID), rootWrite, proposalWrite}})
		if err == nil {
			return nil
		}
		if stored, readErr := s.read(ctx, proposal.PK, proposal.SK); readErr == nil && stored.Fingerprint == proposal.Fingerprint {
			return nil
		}
	}
	return domain.ErrConflict
}

func (s Service) finishGroupDepositPreparation(ctx context.Context, tripID, commandID string) error {
	root, err := s.read(ctx, "TRIP#"+tripID, "GROUP_DEPOSIT#"+commandID)
	if err != nil {
		return err
	}
	if root.Status == "APPLYING" || root.Status == "APPLIED" {
		return nil
	}
	if root.Status != "PREPARING" || root.Expected < 1 || root.Prepared != root.Expected {
		return domain.ErrConflict
	}
	previous := root.Version
	root.Version++
	root.Status = "APPLYING"
	write, err := s.put(root, "#version = :previous", versionValue(previous))
	if err != nil {
		return err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.applyingGroupCheck(tripID, commandID), write}})
	if err != nil {
		return domain.ErrConflict
	}
	return nil
}

func (s Service) allGroupDepositAllocations(ctx context.Context, tripID, commandID string) ([]record, error) {
	db, ok := s.DB.(queryDatabase)
	if !ok {
		return nil, domain.ErrInvalid
	}
	pk, prefix := "TRIP#"+tripID, "GROUP_DEPOSIT#"+commandID+"#ALLOCATION#"
	rows := []record{}
	var cursor map[string]types.AttributeValue
	for {
		out, err := db.Query(ctx, &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}, ":prefix": &types.AttributeValueMemberS{Value: prefix}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(50), ExclusiveStartKey: cursor})
		if err != nil {
			return nil, err
		}
		for _, raw := range out.Items {
			var row record
			if attributevalue.UnmarshalMap(raw, &row) != nil || row.Change == nil || !strings.HasPrefix(row.SK, prefix) {
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

func (s Service) applyGroupDepositAllocation(ctx context.Context, tripID, commandID string, proposal record) error {
	effectSK := "GROUP_DEPOSIT#" + commandID + "#EFFECT#" + proposal.AccountID
	if effect, err := s.read(ctx, "TRIP#"+tripID, effectSK); err == nil {
		if effect.Fingerprint == proposal.Fingerprint {
			return nil
		}
		return ErrReplayMismatch
	}
	change := *proposal.Change
	for retry := 0; retry < 6; retry++ {
		root, err := s.read(ctx, "TRIP#"+tripID, "GROUP_DEPOSIT#"+commandID)
		if err != nil || root.Status != "APPLYING" || root.Applied >= root.Expected {
			return domain.ErrConflict
		}
		account, err := s.read(ctx, "ACCOUNT#"+proposal.AccountID, "META")
		if err != nil || account.Account == nil || account.Account.Version+1 != change.Account.Version {
			return domain.ErrConflict
		}
		rootPrevious := root.Version
		root.Version++
		root.Applied++
		rootWrite, err := s.put(root, "#version = :previous", versionValue(rootPrevious))
		if err != nil {
			return err
		}
		accountWrite, err := s.put(record{PK: account.PK, SK: account.SK, Version: change.Account.Version, Account: &change.Account}, "#version = :previous", versionValue(account.Account.Version))
		if err != nil {
			return err
		}
		eventWrite, err := s.put(record{PK: account.PK, SK: "EVENT#" + fmt.Sprintf("%020d", change.Account.Version), Event: &change.Event}, "attribute_not_exists(pk)", nil)
		if err != nil {
			return err
		}
		effectWrite, err := s.put(record{PK: root.PK, SK: effectSK, AccountID: proposal.AccountID, Fingerprint: proposal.Fingerprint, Change: &change, Status: "APPLIED"}, "attribute_not_exists(pk)", nil)
		if err != nil {
			return err
		}
		member, err := s.read(ctx, root.PK, "MEMBER#"+proposal.AccountID)
		if err != nil || member.Roster == nil {
			return domain.ErrConflict
		}
		roster := *member.Roster
		roster.Version = change.Account.Version
		memberWrite, err := s.put(record{PK: root.PK, SK: member.SK, TripID: tripID, AccountID: proposal.AccountID, Version: change.Account.Version, Roster: &roster}, "#version = :previous", versionValue(member.Version))
		if err != nil {
			return err
		}
		writes := []types.TransactWriteItem{s.applyingGroupCheck(tripID, commandID), rootWrite, accountWrite, memberWrite, eventWrite, effectWrite}
		for _, projection := range cashProjectionRecords(change.Event) {
			projectionWrite, buildErr := s.put(projection, "attribute_not_exists(pk)", nil)
			if buildErr != nil {
				return buildErr
			}
			writes = append(writes, projectionWrite)
		}
		_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
		if err == nil {
			return nil
		}
		if effect, readErr := s.read(ctx, root.PK, effectSK); readErr == nil && effect.Fingerprint == proposal.Fingerprint {
			return nil
		}
	}
	return domain.ErrConflict
}

func (s Service) publishGroupDeposit(ctx context.Context, tripID, commandID string) error {
	for retry := 0; retry < 6; retry++ {
		plan, err := s.read(ctx, "TRIP#"+tripID, "META")
		if err != nil {
			return err
		}
		root, err := s.read(ctx, plan.PK, "GROUP_DEPOSIT#"+commandID)
		if err != nil {
			return err
		}
		if root.Status == "APPLIED" && plan.Status == "ACTIVE" && plan.PendingGroupID == "" {
			return nil
		}
		if plan.Status != "APPLYING_GROUP_DEPOSIT" || plan.PendingGroupID != commandID || root.Status != "APPLYING" || root.Applied != root.Expected {
			return domain.ErrConflict
		}
		planPrevious, rootPrevious := plan.Version, root.Version
		plan.Version++
		plan.Status, plan.PendingGroupID = "ACTIVE", ""
		root.Version++
		root.Status = "APPLIED"
		planWrite, err := s.put(plan, "#version = :previous", versionValue(planPrevious))
		if err != nil {
			return err
		}
		rootWrite, err := s.put(root, "#version = :previous", versionValue(rootPrevious))
		if err != nil {
			return err
		}
		receiptID := commandID
		receiptWrite, buildErr := s.put(record{PK: "RECEIPT#" + receiptID, SK: "META", ReceiptID: receiptID, ReceiptEmail: root.ReceiptEmail, Event: root.Event, Status: "PENDING_DOCUMENT", DocumentVersion: 2}, "attribute_not_exists(pk)", nil)
		if buildErr != nil {
			return buildErr
		}
		tripReceiptWrite, buildErr := s.put(record{PK: plan.PK, SK: "RECEIPT#" + receiptID, ReceiptID: receiptID, TripID: tripID, Event: &domain.Event{Type: root.Event.Type, Amount: root.Event.Amount, EffectiveDate: root.Event.EffectiveDate}}, "attribute_not_exists(pk)", nil)
		if buildErr != nil {
			return buildErr
		}
		jobWrite, buildErr := s.put(record{PK: "JOB#" + root.Event.RecordedAt.UTC().Format("2006-01-02"), SK: "PENDING#" + receiptID, ReceiptID: receiptID, Status: "PENDING"}, "attribute_not_exists(pk)", nil)
		if buildErr != nil {
			return buildErr
		}
		_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{planWrite, rootWrite, receiptWrite, tripReceiptWrite, jobWrite}})
		if err == nil {
			return nil
		}
	}
	return domain.ErrConflict
}

func groupDepositState(root record) GroupDepositState {
	amount := int64(0)
	if root.Event != nil {
		amount = root.Event.Amount
	}
	return GroupDepositState{ID: strings.TrimPrefix(root.SK, "GROUP_DEPOSIT#"), TripID: root.TripID, Status: root.Status, Amount: amount, Prepared: root.Prepared, Applied: root.Applied, Expected: root.Expected}
}
