package functions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	budget "ind-hub-api-gox-sls-pri-gh/services/api-analytics/domain/analytics"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type analyticsRecord struct {
	PK      string `dynamodbav:"pk"`
	SK      string `dynamodbav:"sk"`
	Version int64  `dynamodbav:"version"`
	Data    string `dynamodbav:"data"`
	Hash    string `dynamodbav:"hash,omitempty"`
	Actor   string `dynamodbav:"actor,omitempty"`
}

func (s Service) analyticsRead(ctx context.Context, tripID, sk string) (budget.State, analyticsRecord, error) {
	out, err := s.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.Table, Key: key("TRIP#"+tripID, sk), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return budget.State{}, analyticsRecord{}, err
	}
	if len(out.Item) == 0 {
		return budget.State{Scenarios: map[string]budget.Budget{}}, analyticsRecord{}, ErrNotFound
	}
	var row analyticsRecord
	if err = attributevalue.UnmarshalMap(out.Item, &row); err != nil {
		return budget.State{}, row, err
	}
	var state budget.State
	err = json.Unmarshal([]byte(row.Data), &state)
	return state, row, err
}

// SaveAnalytics preserva revisiones y deduplica comandos con actor e intención exactos.
func (s Service) SaveAnalytics(ctx context.Context, tripID string, c budget.Command, actor string, now time.Time) (budget.State, error) {
	if !validPortalID(tripID) {
		return budget.State{}, domain.ErrInvalid
	}
	encoded, err := json.Marshal(c)
	if err != nil || len(encoded) > 190000 {
		return budget.State{}, domain.ErrInvalid
	}
	sum := sha256.Sum256(append([]byte(actor+":"), encoded...))
	hash := hex.EncodeToString(sum[:])
	commandSK := "ANALYTICS#COMMAND#" + c.CommandID
	if prior, row, e := s.analyticsRead(ctx, tripID, commandSK); e == nil {
		if row.Hash != hash {
			return budget.State{}, ErrReplayMismatch
		}
		return prior, nil
	} else if !errors.Is(e, ErrNotFound) {
		return budget.State{}, e
	}
	state, _, err := s.analyticsRead(ctx, tripID, "ANALYTICS#CURRENT")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return budget.State{}, err
	}
	if state.Version != c.Version {
		return budget.State{}, domain.ErrConflict
	}
	validationBudget := c.Budget
	if c.Action == "PROMOTE" {
		if candidate, ok := state.Scenarios[c.ScenarioID]; ok {
			validationBudget = &candidate
		} else {
			return budget.State{}, domain.ErrInvalid
		}
	}
	if validationBudget != nil {
		cBudget := validationBudget

		if cBudget.Validate() != nil {
			return budget.State{}, domain.ErrInvalid
		}
		if cBudget.Source.Kind == "contract" && cBudget.Source.ID != tripID {
			return budget.State{}, domain.ErrInvalid
		}
		allocated := map[string]float64{}
		ids := map[string]bool{}
		for _, line := range cBudget.Lines {
			if line.SupplierID != "" {
				ids[line.SupplierID] = true
			}
			for _, allocation := range line.Allocations {
				ids[allocation.SupplierID] = true
				allocated[allocation.SupplierID] += allocation.Amount
			}
		}
		for id := range ids {
			row, e := s.read(ctx, "TRIP#"+tripID, "SUPPLIER#"+id)
			if e != nil {
				return budget.State{}, e
			}
			if row.Supplier == nil || row.Supplier.TripID != tripID || allocated[id] > float64(row.Supplier.Committed) {
				return budget.State{}, domain.ErrInvalid
			}
		}
	}
	next, err := budget.Apply(state, c, actor, now)
	if err != nil {
		return budget.State{}, domain.ErrInvalid
	}
	raw, err := json.Marshal(next)
	if err != nil || len(raw) > 290000 {
		return budget.State{}, domain.ErrInvalid
	}
	rows := []analyticsRecord{{PK: "TRIP#" + tripID, SK: "ANALYTICS#CURRENT", Version: next.Version, Data: string(raw)}, {PK: "TRIP#" + tripID, SK: fmt.Sprintf("ANALYTICS#REV#%020d", next.Version), Version: next.Version, Data: string(raw)}, {PK: "TRIP#" + tripID, SK: commandSK, Version: next.Version, Data: string(raw), Hash: hash, Actor: actor}}
	writes := []types.TransactWriteItem{}
	for i, row := range rows {
		item, e := attributevalue.MarshalMap(row)
		if e != nil {
			return budget.State{}, e
		}
		put := &types.Put{TableName: &s.Table, Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")}
		if i == 0 && state.Version > 0 {
			put.ConditionExpression = aws.String("#v = :v")
			put.ExpressionAttributeNames = map[string]string{"#v": "version"}
			put.ExpressionAttributeValues = map[string]types.AttributeValue{":v": &types.AttributeValueMemberN{Value: fmt.Sprint(state.Version)}}
		}
		writes = append(writes, types.TransactWriteItem{Put: put})
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err == nil {
		return next, nil
	}
	if prior, row, e := s.analyticsRead(ctx, tripID, commandSK); e == nil && row.Hash == hash {
		return prior, nil
	}
	var cancelled *types.TransactionCanceledException
	if errors.As(err, &cancelled) {
		return budget.State{}, domain.ErrConflict
	}
	return budget.State{}, err
}

// AnalyticsRevision expone auditoría sin descargar nuevamente todos los presupuestos.
type AnalyticsRevision struct {
	Version int64        `json:"version"`
	Audit   budget.Audit `json:"audit"`
}

func (s Service) analyticsRevisions(ctx context.Context, tripID string) ([]AnalyticsRevision, error) {
	states := []AnalyticsRevision{}
	var cursor map[string]types.AttributeValue
	for {
		out, err := s.DB.Query(ctx, &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "TRIP#" + tripID}, ":prefix": &types.AttributeValueMemberS{Value: "ANALYTICS#REV#"}}, ScanIndexForward: aws.Bool(false), Limit: aws.Int32(20), ConsistentRead: aws.Bool(true), ExclusiveStartKey: cursor})
		if err != nil {
			return nil, err
		}
		for _, item := range out.Items {
			var row analyticsRecord
			if err = attributevalue.UnmarshalMap(item, &row); err != nil {
				return nil, err
			}
			var state budget.State
			if err = json.Unmarshal([]byte(row.Data), &state); err != nil {
				return nil, err
			}
			states = append(states, AnalyticsRevision{Version: state.Version, Audit: state.Audit})
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		cursor = out.LastEvaluatedKey
	}
	return states, nil
}
