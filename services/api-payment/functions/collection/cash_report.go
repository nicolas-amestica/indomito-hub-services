package collection

import (
	"context"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type TripCashMovement struct {
	TripID     string `json:"tripId"`
	Inflows    int64  `json:"inflows"`
	Outflows   int64  `json:"outflows"`
	Net        int64  `json:"net"`
	ActualFees int64  `json:"actualFees"`
}

type ConsolidatedCashView struct {
	From       string             `json:"from"`
	To         string             `json:"to"`
	Inflows    int64              `json:"inflows"`
	Outflows   int64              `json:"outflows"`
	Net        int64              `json:"net"`
	ActualFees int64              `json:"actualFees"`
	Trips      []TripCashMovement `json:"trips"`
}

func (s Service) ConsolidatedCash(ctx context.Context, from, to string) (ConsolidatedCashView, error) {
	fromDate, fromErr := time.Parse(time.DateOnly, from)
	toDate, toErr := time.Parse(time.DateOnly, to)
	if fromErr != nil || toErr != nil || from > to {
		return ConsolidatedCashView{}, domain.ErrInvalid
	}
	months := monthKeys(fromDate, toDate)
	if len(months) == 0 || len(months) > 12 {
		return ConsolidatedCashView{}, domain.ErrInvalid
	}
	db, ok := s.DB.(queryDatabase)
	if !ok {
		return ConsolidatedCashView{}, domain.ErrInvalid
	}
	byTrip := map[string][]domain.Event{}
	for _, month := range months {
		var cursor map[string]types.AttributeValue
		for {
			out, err := db.Query(ctx, &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "CASH#" + month}, ":prefix": &types.AttributeValueMemberS{Value: "TRIP#"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(50), ExclusiveStartKey: cursor})
			if err != nil {
				return ConsolidatedCashView{}, err
			}
			for _, raw := range out.Items {
				var row record
				if attributevalue.UnmarshalMap(raw, &row) != nil || row.Event == nil || row.Event.TripID == "" {
					return ConsolidatedCashView{}, domain.ErrInvalid
				}
				if row.Event.EffectiveDate >= from && row.Event.EffectiveDate <= to {
					byTrip[row.Event.TripID] = append(byTrip[row.Event.TripID], *row.Event)
				}
			}
			if len(out.LastEvaluatedKey) == 0 {
				break
			}
			cursor = out.LastEvaluatedKey
		}
	}
	view := ConsolidatedCashView{From: from, To: to, Trips: []TripCashMovement{}}
	for tripID, events := range byTrip {
		period, err := domain.ProjectCash(0, from, to, tripID, events)
		if err != nil {
			return ConsolidatedCashView{}, err
		}
		trip := TripCashMovement{TripID: tripID, Inflows: period.Inflows, Outflows: period.Outflows, Net: period.Inflows - period.Outflows}
		for _, event := range events {
			for _, entry := range event.Entries {
				if entry.Account == "PAYMENT_FEES" && entry.Amount > 0 {
					trip.ActualFees += entry.Amount
				}
			}
		}
		view.Inflows += trip.Inflows
		view.Outflows += trip.Outflows
		view.ActualFees += trip.ActualFees
		view.Trips = append(view.Trips, trip)
	}
	view.Net = view.Inflows - view.Outflows
	sort.Slice(view.Trips, func(i, j int) bool { return view.Trips[i].TripID < view.Trips[j].TripID })
	return view, nil
}

func monthKeys(from, to time.Time) []string {
	current := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, time.UTC)
	months := []string{}
	for !current.After(last) && len(months) <= 12 {
		months = append(months, current.Format("2006-01"))
		current = current.AddDate(0, 1, 0)
	}
	return months
}
