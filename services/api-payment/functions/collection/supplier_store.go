package collection

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type SupplierOperation struct {
	Type          string `json:"type"`
	Name          string `json:"name,omitempty"`
	Service       string `json:"service,omitempty"`
	Committed     int64  `json:"committed,omitempty"`
	RefundAgreed  int64  `json:"refundAgreed,omitempty"`
	Amount        int64  `json:"amount,omitempty"`
	Reference     string `json:"reference,omitempty"`
	EffectiveDate string `json:"effectiveDate,omitempty"`
	AnnexID       string `json:"annexId,omitempty"`
}

type SupplierPage struct {
	Items      []domain.SupplierCommitment `json:"items"`
	NextCursor string                      `json:"nextCursor,omitempty"`
}

func (s Service) ListSuppliers(ctx context.Context, tripID, cursor string) (SupplierPage, error) {
	if !validPortalID(tripID) || (cursor != "" && !validPortalID(cursor)) {
		return SupplierPage{}, domain.ErrInvalid
	}
	db, ok := s.DB.(queryDatabase)
	if !ok {
		return SupplierPage{}, domain.ErrInvalid
	}
	input := &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "TRIP#" + tripID}, ":prefix": &types.AttributeValueMemberS{Value: "SUPPLIER#"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(20)}
	if cursor != "" {
		input.ExclusiveStartKey = key("TRIP#"+tripID, "SUPPLIER#"+cursor)
	}
	out, err := db.Query(ctx, input)
	if err != nil {
		return SupplierPage{}, err
	}
	page := SupplierPage{Items: []domain.SupplierCommitment{}}
	for _, raw := range out.Items {
		var row record
		if attributevalue.UnmarshalMap(raw, &row) != nil || row.Supplier == nil {
			return SupplierPage{}, domain.ErrInvalid
		}
		page.Items = append(page.Items, *row.Supplier)
	}
	if sk, ok := out.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
		page.NextCursor = strings.TrimPrefix(sk.Value, "SUPPLIER#")
	}
	return page, nil
}

func (s Service) ApplySupplierOperation(ctx context.Context, tripID, supplierID string, expectedVersion int64, operation SupplierOperation, audit domain.Audit) (domain.SupplierCommitment, domain.Event, error) {
	commandHash, err := fingerprint(Command{ID: audit.CommandID, AccountID: tripID, ExpectedVersion: expectedVersion, Reference: operation.Reference, Payload: operation})
	if err != nil {
		return domain.SupplierCommitment{}, domain.Event{}, err
	}
	if prior, readErr := s.read(ctx, "COMMAND#"+audit.CommandID, "META"); readErr == nil {
		if prior.Fingerprint != commandHash || prior.Supplier == nil || prior.Event == nil {
			return domain.SupplierCommitment{}, domain.Event{}, ErrReplayMismatch
		}
		return *prior.Supplier, *prior.Event, nil
	} else if !errors.Is(readErr, ErrNotFound) {
		return domain.SupplierCommitment{}, domain.Event{}, readErr
	}
	if !validPortalID(tripID) || !validPortalID(supplierID) {
		return domain.SupplierCommitment{}, domain.Event{}, domain.ErrInvalid
	}
	if _, err = s.read(ctx, "TRIP#"+tripID, "META"); err != nil {
		return domain.SupplierCommitment{}, domain.Event{}, err
	}
	pk, sk := "TRIP#"+tripID, "SUPPLIER#"+supplierID
	var next domain.SupplierCommitment
	var event domain.Event
	if operation.Type == "CREATE" {
		if expectedVersion != 0 {
			return domain.SupplierCommitment{}, domain.Event{}, domain.ErrInvalid
		}
		next, event, err = domain.CreateSupplierCommitment(supplierID, tripID, operation.Name, operation.Service, operation.Committed, audit)
	} else {
		row, readErr := s.read(ctx, pk, sk)
		if readErr != nil || row.Supplier == nil {
			return domain.SupplierCommitment{}, domain.Event{}, readErr
		}
		if row.Supplier.Version != expectedVersion {
			return domain.SupplierCommitment{}, domain.Event{}, domain.ErrConflict
		}
		switch operation.Type {
		case "REVISE":
			next, event, err = domain.ReviseSupplier(*row.Supplier, audit, operation.Committed, operation.RefundAgreed, operation.AnnexID)
		case "PAY":
			next, event, err = domain.PaySupplier(*row.Supplier, audit, operation.Amount, operation.Reference, operation.EffectiveDate)
		case "RECEIVE_REFUND":
			next, event, err = domain.ReceiveSupplierRefund(*row.Supplier, audit, operation.Amount, operation.Reference, operation.EffectiveDate)
		default:
			err = domain.ErrInvalid
		}
	}
	if err != nil {
		return domain.SupplierCommitment{}, domain.Event{}, err
	}
	rows := []record{{PK: pk, SK: sk, TripID: tripID, Version: next.Version, Supplier: &next}, {PK: "COMMAND#" + audit.CommandID, SK: "META", Fingerprint: commandHash, Supplier: &next, Event: &event}, {PK: pk, SK: "SUPPLIER_EVENT#" + supplierID + "#" + fmt.Sprintf("%020d", next.Version), TripID: tripID, Event: &event}}
	if operation.Reference != "" {
		rows = append(rows, record{PK: "REFERENCE#" + operation.Reference, SK: "META", Fingerprint: commandHash, TripID: tripID})
	}
	rows = append(rows, cashProjectionRecords(event)...)
	writes := make([]types.TransactWriteItem, 0, len(rows))
	for index, item := range rows {
		condition := "attribute_not_exists(pk)"
		var values map[string]types.AttributeValue
		if index == 0 && expectedVersion > 0 {
			condition = "#version = :previous"
			values = versionValue(expectedVersion)
		}
		write, buildErr := s.put(item, condition, values)
		if buildErr != nil {
			return domain.SupplierCommitment{}, domain.Event{}, buildErr
		}
		writes = append(writes, write)
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err == nil {
		return next, event, nil
	}
	if prior, readErr := s.read(ctx, "COMMAND#"+audit.CommandID, "META"); readErr == nil && prior.Fingerprint == commandHash && prior.Supplier != nil && prior.Event != nil {
		return *prior.Supplier, *prior.Event, nil
	}
	if operation.Reference != "" {
		if _, readErr := s.read(ctx, "REFERENCE#"+operation.Reference, "META"); readErr == nil {
			return domain.SupplierCommitment{}, domain.Event{}, ErrReferenceUsed
		}
	}
	return domain.SupplierCommitment{}, domain.Event{}, domain.ErrConflict
}
