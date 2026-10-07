package collection

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type contractIndexDB struct {
	*transactionDB
	items []contractAlertSummary
}

func (d *contractIndexDB) Query(ctx context.Context, input *dynamodb.QueryInput, options ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	if input.IndexName == nil {
		return d.transactionDB.Query(ctx, input, options...)
	}
	out := &dynamodb.QueryOutput{}
	for _, item := range d.items {
		raw, err := attributevalue.MarshalMap(item)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, raw)
	}
	return out, nil
}

func TestGlobalAlertsReuseContractYearIndexAndSkipPlansNotStarted(t *testing.T) {
	payments, _, input, _ := groupDepositFixture(t)
	missing := paymentOperationID("approved-without-plan")
	contracts := &contractIndexDB{transactionDB: &transactionDB{items: map[string]map[string]types.AttributeValue{}}, items: []contractAlertSummary{{ID: input.TripID, Status: "APPROVED", InstitutionName: "Colegio Sur", Destination: "Bariloche"}, {ID: missing, Status: "APPROVED"}, {ID: paymentOperationID("draft"), Status: "DRAFT"}}}
	app := &AdminApp{Accounts: payments, Contracts: ContractSource{DB: contracts, Table: "contracts"}}
	req := adminRequest("GET", "")
	req.QueryStringParameters = map[string]string{"year": "2027", "asOf": "2027-02-01"}
	response, err := handleGlobalCollectionAlerts(context.Background(), req, app)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	var envelope struct {
		Data GlobalCollectionAlerts `json:"data"`
	}
	if json.Unmarshal([]byte(response.Body), &envelope) != nil {
		t.Fatal(response.Body)
	}
	if len(envelope.Data.Groups) != 1 || envelope.Data.Groups[0].TripID != input.TripID || envelope.Data.Overdue != 2700 {
		t.Fatalf("unexpected global alerts: %+v", envelope.Data)
	}
}
