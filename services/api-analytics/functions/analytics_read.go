package functions

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	budget "ind-hub-api-gox-sls-pri-gh/services/api-analytics/domain/analytics"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"sort"
	"time"
)

// AnalyticsTrip identifica el viaje por contrato aprobado y preserva el significado de las cantidades.
type AnalyticsTrip struct {
	ID              string `json:"id" dynamodbav:"id"`
	Version         int64  `json:"version" dynamodbav:"version"`
	Status          string `json:"status" dynamodbav:"status"`
	InstitutionName string `json:"institutionName" dynamodbav:"institutionName"`
	Destination     string `json:"destination" dynamodbav:"destination"`
	Period          string `json:"period" dynamodbav:"period"`
	Content         struct {
		Institution struct {
			Course string `json:"course" dynamodbav:"course"`
		} `json:"institution" dynamodbav:"institution"`
		Trip struct {
			DepartureDate string `json:"departureDate" dynamodbav:"departureDate"`
			Days          int    `json:"days" dynamodbav:"days"`
			Nights        int    `json:"nights" dynamodbav:"nights"`
		} `json:"trip" dynamodbav:"trip"`
		Payments struct {
			TotalPassengers int   `json:"totalPassengers" dynamodbav:"totalPassengers"`
			FreePassengers  int   `json:"freePassengers" dynamodbav:"freePassengers"`
			TotalGroup      int64 `json:"totalGroup" dynamodbav:"totalGroup"`
			PricePerPerson  int64 `json:"pricePerPerson" dynamodbav:"pricePerPerson"`
		} `json:"payments" dynamodbav:"payments"`
		Passengers []struct{} `json:"-" dynamodbav:"passengers"`
	} `json:"content" dynamodbav:"content"`
	ProgramReference *struct {
		ID        string                 `json:"id" dynamodbav:"id"`
		UpdatedAt string                 `json:"updatedAt" dynamodbav:"updatedAt"`
		Content   map[string]interface{} `json:"content" dynamodbav:"content"`
	} `json:"programReference" dynamodbav:"programReference"`
	RosterCount        int  `json:"rosterCount" dynamodbav:"-"`
	QuantityConsistent bool `json:"quantityConsistent" dynamodbav:"-"`
}

// AnalyticsFinance agrega cuentas en servidor; nunca entrega RUT ni nómina al dashboard.
type AnalyticsFinance struct {
	ContractedSale int64 `json:"contractedSale"`

	ReceiptHistory  []AnalyticsReceipt `json:"receiptHistory"`
	HistoryComplete bool               `json:"historyComplete"`

	Position         domain.Position             `json:"position"`
	Overdue          int64                       `json:"overdue"`
	Aging            [4]int64                    `json:"aging"`
	Suppliers        []domain.SupplierCommitment `json:"suppliers"`
	CashDays         []AnalyticsCashDay          `json:"cashDays"`
	Opening          int64                       `json:"opening"`
	Closing          int64                       `json:"closing"`
	Inflows          int64                       `json:"inflows"`
	Outflows         int64                       `json:"outflows"`
	ActualFees       int64                       `json:"actualFees"`
	Scheduled        []AnalyticsDue              `json:"scheduled"`
	ActivePassengers int                         `json:"activePassengers"`
	FreePassengers   int                         `json:"freePassengers"`
}

// AnalyticsCashDay agrupa entradas/salidas bancarias sin exponer diarios por pasajero.
type AnalyticsCashDay struct {
	Date     string `json:"date"`
	Inflows  int64  `json:"inflows"`
	Outflows int64  `json:"outflows"`
}

// AnalyticsReceipt es un cobro aplicado a cuotas, independiente de la fecha bancaria.
type AnalyticsReceipt struct {
	Date   string `json:"date"`
	Amount int64  `json:"amount"`
}

// AnalyticsDue muestra saldo de cuota actual con vencimiento, sin inventar un histórico.
type AnalyticsDue struct {
	Date     string `json:"date"`
	Original int64  `json:"original"`
	Pending  int64  `json:"pending"`
}

func (a AdminApp) analyticsTrip(ctx context.Context, id string) (AnalyticsTrip, error) {
	out, err := a.Contracts.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: &a.Contracts.Table, Key: key("CONTRACT#"+id, "METADATA"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return AnalyticsTrip{}, err
	}
	if len(out.Item) == 0 {
		return AnalyticsTrip{}, ErrNotFound
	}
	var trip AnalyticsTrip
	if err = attributevalue.UnmarshalMap(out.Item, &trip); err != nil {
		return trip, err
	}
	if trip.ID != id || trip.Status != "APPROVED" {
		return trip, domain.ErrInvalid
	}
	termsOut, termsErr := a.Contracts.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: &a.Contracts.Table, Key: key("CONTRACT#"+id, "TERMS#CURRENT"), ConsistentRead: aws.Bool(true)})
	if termsErr != nil {
		return trip, termsErr
	}
	if len(termsOut.Item) > 0 {
		var current struct {
			Terms struct {
				DepartureDate string `dynamodbav:"departureDate"`
				Days          int    `dynamodbav:"days"`
				Nights        int    `dynamodbav:"nights"`
			} `dynamodbav:"terms"`
		}
		if err = attributevalue.UnmarshalMap(termsOut.Item, &current); err != nil {
			return trip, err
		}
		trip.Content.Trip.DepartureDate = current.Terms.DepartureDate
		trip.Content.Trip.Days = current.Terms.Days
		trip.Content.Trip.Nights = current.Terms.Nights
	}
	if trip.ProgramReference != nil {
		source := trip.ProgramReference.Content
		safe := map[string]interface{}{}
		for _, name := range []string{"pricing", "schedule", "totals", "services", "crews"} {
			if value, ok := source[name]; ok {
				safe[name] = value
			}
		}
		if crews, ok := safe["crews"].([]interface{}); ok {
			for _, crew := range crews {
				if person, ok := crew.(map[string]interface{}); ok {
					delete(person, "documentId")
				}
			}
		}
		trip.ProgramReference.Content = safe
	}
	trip.RosterCount = len(trip.Content.Passengers)
	trip.QuantityConsistent = trip.Content.Payments.TotalPassengers > 0 && trip.Content.Payments.FreePassengers >= 0 && trip.RosterCount == trip.Content.Payments.TotalPassengers+trip.Content.Payments.FreePassengers
	return trip, nil
}

func (s Service) analyticsFinance(ctx context.Context, id, from, to string) (*AnalyticsFinance, string, error) {
	if _, err := s.read(ctx, "TRIP#"+id, "SETUP"); errors.Is(err, ErrNotFound) {
		return nil, "Cobranza aún no habilitada para este viaje", nil
	} else if err != nil {
		return nil, "", err
	}
	result := &AnalyticsFinance{HistoryComplete: true, ReceiptHistory: []AnalyticsReceipt{}, Suppliers: []domain.SupplierCommitment{}, CashDays: []AnalyticsCashDay{}, Scheduled: []AnalyticsDue{}}
	cursor := ""
	members := 0
	end, err := time.Parse(time.DateOnly, to)
	if err != nil {
		return nil, "", err
	}
	for {
		page, err := s.ListRoster(ctx, id, cursor)
		if err != nil {
			return nil, "", err
		}
		for _, member := range page.Items {
			members++
			account, e := s.GetAccount(ctx, member.AccountID)
			if e != nil {
				return nil, "", e
			}
			if account.TripID != id {
				return nil, "", domain.ErrInvalid
			}
			history, complete, historyErr := s.receiptHistory(ctx, account)
			if historyErr != nil {
				return nil, "", historyErr
			}
			result.ReceiptHistory = append(result.ReceiptHistory, history...)
			result.HistoryComplete = result.HistoryComplete && complete
			p := account.Position()
			result.ContractedSale += p.AppliedReceipts + p.Receivable - account.WithdrawalRefundApproved
			result.Position.Receivable += p.Receivable
			result.Position.DepositReceivable += p.DepositReceivable
			result.Position.InstallmentReceivable += p.InstallmentReceivable
			result.Position.AppliedReceipts += p.AppliedReceipts
			result.Position.UnappliedReceipts += p.UnappliedReceipts
			result.Position.Discounts += p.Discounts
			result.Position.Cancelled += p.Cancelled
			result.Position.RefundPayable += p.RefundPayable
			result.Position.Refunded += p.Refunded
			if account.Active {
				result.ActivePassengers++
				if account.Free {
					result.FreePassengers++
				}
			}
			for _, i := range account.Installments {
				pending := i.Outstanding()
				result.Scheduled = append(result.Scheduled, AnalyticsDue{Date: i.DueDate, Original: i.Original - i.Discount - i.Cancelled, Pending: pending})
				if pending > 0 && i.DueDate < to {
					date, e := time.Parse(time.DateOnly, i.DueDate)
					if e != nil {
						return nil, "", e
					}
					days := int(end.Sub(date).Hours() / 24)
					bucket := 0
					if days > 90 {
						bucket = 3
					} else if days > 60 {
						bucket = 2
					} else if days > 30 {
						bucket = 1
					}
					result.Aging[bucket] += pending
					result.Overdue += pending
				}
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if members == 0 {
		return nil, "", domain.ErrInvalid
	}
	cursor = ""
	for {
		page, e := s.ListSuppliers(ctx, id, cursor)
		if e != nil {
			return nil, "", e
		}
		result.Suppliers = append(result.Suppliers, page.Items...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	db, ok := s.DB.(queryDatabase)
	if !ok {
		return nil, "", domain.ErrInvalid
	}
	var start map[string]types.AttributeValue
	all := []domain.Event{}
	for {
		out, e := db.Query(ctx, &dynamodb.QueryInput{TableName: &s.PaymentsTable, KeyConditionExpression: aws.String("pk = :pk AND sk BETWEEN :from AND :to"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "TRIP#" + id}, ":from": &types.AttributeValueMemberS{Value: "CASH#0000"}, ":to": &types.AttributeValueMemberS{Value: "CASH#" + to + "~"}}, ConsistentRead: aws.Bool(true), ExclusiveStartKey: start})
		if e != nil {
			return nil, "", e
		}
		for _, item := range out.Items {
			var row record
			if e = attributevalue.UnmarshalMap(item, &row); e != nil || row.Event == nil {
				return nil, "", domain.ErrInvalid
			}
			event := *row.Event
			all = append(all, event)

		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	first, _ := time.Parse(time.DateOnly, from)
	opening, e := domain.ProjectCash(0, "1900-01-01", first.AddDate(0, 0, -1).Format(time.DateOnly), id, all)
	if e != nil {
		return nil, "", e
	}
	period, e := domain.ProjectCash(opening.Closing, from, to, id, all)
	if e != nil {
		return nil, "", e
	}
	scheduled := map[string]AnalyticsDue{}
	for _, due := range result.Scheduled {
		value := scheduled[due.Date]
		value.Date = due.Date
		value.Original += due.Original
		value.Pending += due.Pending
		scheduled[due.Date] = value
	}
	result.Scheduled = []AnalyticsDue{}
	for _, value := range scheduled {
		result.Scheduled = append(result.Scheduled, value)
	}
	sort.Slice(result.Scheduled, func(i, j int) bool { return result.Scheduled[i].Date < result.Scheduled[j].Date })
	history := map[string]int64{}
	for _, receipt := range result.ReceiptHistory {
		history[receipt.Date] += receipt.Amount
	}
	result.ReceiptHistory = []AnalyticsReceipt{}
	for date, amount := range history {
		result.ReceiptHistory = append(result.ReceiptHistory, AnalyticsReceipt{Date: date, Amount: amount})
	}
	sort.Slice(result.ReceiptHistory, func(i, j int) bool { return result.ReceiptHistory[i].Date < result.ReceiptHistory[j].Date })
	cashDays := map[string]AnalyticsCashDay{}
	for _, event := range all {
		if event.EffectiveDate < from || event.EffectiveDate > to {
			continue
		}
		value := cashDays[event.EffectiveDate]
		value.Date = event.EffectiveDate
		for _, entry := range event.Entries {
			if entry.Account == "BANK" {
				if entry.Amount > 0 {
					value.Inflows += entry.Amount
				} else {
					value.Outflows -= entry.Amount
				}
			}
		}
		cashDays[event.EffectiveDate] = value
	}
	for _, value := range cashDays {
		result.CashDays = append(result.CashDays, value)
	}
	sort.Slice(result.CashDays, func(i, j int) bool { return result.CashDays[i].Date < result.CashDays[j].Date })
	result.Opening = period.Opening
	result.Closing = period.Closing
	result.Inflows = period.Inflows
	result.Outflows = period.Outflows
	cursor = ""
	for {
		page, e := s.ListSettlements(ctx, id, cursor)
		if e != nil {
			return nil, "", e
		}
		for _, settlement := range page.Items {
			result.ActualFees += settlement.ActualFees
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return result, "", nil
}

// AnalyticsRow mantiene explícitos los fallos parciales por viaje.
type AnalyticsRow struct {
	FinanceStatus string `json:"financeStatus"`

	Trip    AnalyticsTrip     `json:"trip"`
	State   budget.State      `json:"state"`
	Finance *AnalyticsFinance `json:"finance"`
	Issue   string            `json:"issue"`
}

// receiptHistory verifica cobertura contra lo pagado hoy antes de habilitar una curva histórica.
func (s Service) receiptHistory(ctx context.Context, account domain.Account) ([]AnalyticsReceipt, bool, error) {
	rows := []AnalyticsReceipt{}
	complete := true
	var cursor map[string]types.AttributeValue
	var sum int64
	for {
		out, err := s.DB.Query(ctx, &dynamodb.QueryInput{TableName: &s.PaymentsTable, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "ACCOUNT#" + account.ID}, ":prefix": &types.AttributeValueMemberS{Value: "EVENT#"}}, ConsistentRead: aws.Bool(true), ExclusiveStartKey: cursor})
		if err != nil {
			return nil, false, err
		}
		for _, item := range out.Items {
			var row record
			if err = attributevalue.UnmarshalMap(item, &row); err != nil || row.Event == nil {
				return nil, false, domain.ErrInvalid
			}
			e := row.Event
			amount := int64(0)
			if e.Type == "PAYMENT_RECEIVED" && e.InstallmentID != "" {
				amount = e.Amount
			}
			if e.Type == "PAYMENT_REVERSED_BY_PROVIDER" && e.InstallmentID != "" {
				for _, entry := range e.Entries {
					if entry.Account == "CUSTOMER_FUNDS" {
						amount -= entry.Amount
					}
				}
			}
			if e.Type == "UNAPPLIED_FUNDS_ALLOCATED" {
				complete = false
			}
			if amount != 0 {
				if _, err = time.Parse(time.DateOnly, e.EffectiveDate); err != nil {
					complete = false
					continue
				}
				rows = append(rows, AnalyticsReceipt{Date: e.EffectiveDate, Amount: amount})
				sum += amount
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		cursor = out.LastEvaluatedKey
	}
	paid := int64(0)
	for _, i := range account.Installments {
		paid += i.Paid
	}
	return rows, complete && sum == paid, nil
}
