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

func cashProjectionRecords(event domain.Event) []record {
	if event.TripID == "" || len(event.EffectiveDate) != 10 || len(event.Entries) == 0 {
		return nil
	}
	month := event.EffectiveDate[:7]
	suffix := event.EffectiveDate + "#" + event.CommandID
	return []record{{PK: "TRIP#" + event.TripID, SK: "CASH#" + suffix, TripID: event.TripID, Event: &event}, {PK: "CASH#" + month, SK: "TRIP#" + event.TripID + "#" + suffix, TripID: event.TripID, Event: &event}}
}

type SettlementPage struct {
	Items      []domain.Settlement `json:"items"`
	NextCursor string              `json:"nextCursor,omitempty"`
}

func (s Service) ListSettlements(ctx context.Context, tripID, cursor string) (SettlementPage, error) {
	if !validPortalID(tripID) || (cursor != "" && !strings.HasPrefix(cursor, "khipu:")) {
		return SettlementPage{}, domain.ErrInvalid
	}
	db, ok := s.DB.(queryDatabase)
	if !ok {
		return SettlementPage{}, domain.ErrInvalid
	}
	input := &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "TRIP#" + tripID}, ":prefix": &types.AttributeValueMemberS{Value: "SETTLEMENT#"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(20)}
	if cursor != "" {
		input.ExclusiveStartKey = key("TRIP#"+tripID, "SETTLEMENT#"+cursor)
	}
	out, err := db.Query(ctx, input)
	if err != nil {
		return SettlementPage{}, err
	}
	page := SettlementPage{Items: []domain.Settlement{}}
	for _, raw := range out.Items {
		var row record
		if attributevalue.UnmarshalMap(raw, &row) != nil || row.Settlement == nil {
			return SettlementPage{}, domain.ErrInvalid
		}
		page.Items = append(page.Items, *row.Settlement)
	}
	if sk, ok := out.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
		page.NextCursor = strings.TrimPrefix(sk.Value, "SETTLEMENT#")
	}
	return page, nil
}

func (s Service) ReconcileSettlement(ctx context.Context, tripID, paymentReference string, version, gross, fee int64, bankReference, effectiveDate string, audit domain.Audit) (domain.Settlement, domain.Event, error) {
	pk, sk := "TRIP#"+tripID, "SETTLEMENT#"+paymentReference
	commandHash, err := fingerprint(Command{ID: audit.CommandID, AccountID: tripID, ExpectedVersion: version, Reference: bankReference, Payload: struct {
		Payment    string
		Gross, Fee int64
		Date       string
	}{paymentReference, gross, fee, effectiveDate}})
	if err != nil {
		return domain.Settlement{}, domain.Event{}, err
	}
	if prior, e := s.read(ctx, "COMMAND#"+audit.CommandID, "META"); e == nil {
		if prior.Fingerprint != commandHash || prior.Event == nil {
			return domain.Settlement{}, domain.Event{}, ErrReplayMismatch
		}
		current, readErr := s.read(ctx, pk, sk)
		if readErr != nil || current.Settlement == nil {
			return domain.Settlement{}, domain.Event{}, readErr
		}
		return *current.Settlement, *prior.Event, nil
	} else if !errors.Is(e, ErrNotFound) {
		return domain.Settlement{}, domain.Event{}, e
	}
	row, err := s.read(ctx, pk, sk)
	if err != nil || row.Settlement == nil {
		return domain.Settlement{}, domain.Event{}, err
	}
	if row.Settlement.Version != version {
		return domain.Settlement{}, domain.Event{}, domain.ErrConflict
	}
	next, event, err := domain.ReconcileSettlement(*row.Settlement, audit, gross, fee, bankReference, effectiveDate)
	if err != nil {
		return domain.Settlement{}, domain.Event{}, err
	}
	rows := []record{{PK: pk, SK: sk, TripID: tripID, Version: next.Version, Settlement: &next}, {PK: "COMMAND#" + audit.CommandID, SK: "META", Fingerprint: commandHash, Event: &event}, {PK: pk, SK: "TREASURY_EVENT#" + audit.CommandID, TripID: tripID, Event: &event}, {PK: "REFERENCE#" + bankReference, SK: "META", Fingerprint: commandHash, TripID: tripID}}
	rows = append(rows, cashProjectionRecords(event)...)
	writes := make([]types.TransactWriteItem, 0, len(rows))
	for index, item := range rows {
		condition := "attribute_not_exists(pk)"
		var values map[string]types.AttributeValue
		if index == 0 {
			condition = "#version = :previous"
			values = versionValue(version)
		}
		write, e := s.put(item, condition, values)
		if e != nil {
			return domain.Settlement{}, domain.Event{}, e
		}
		writes = append(writes, write)
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err != nil {
		if prior, e := s.read(ctx, "COMMAND#"+audit.CommandID, "META"); e == nil && prior.Fingerprint == commandHash {
			return next, event, nil
		}
		if _, e := s.read(ctx, "REFERENCE#"+bankReference, "META"); e == nil {
			return domain.Settlement{}, domain.Event{}, ErrReferenceUsed
		}
		return domain.Settlement{}, domain.Event{}, domain.ErrConflict
	}
	return next, event, nil
}
