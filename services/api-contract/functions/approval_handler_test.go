package functions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/services/api-contract/domain"
)

type approvalDB struct {
	awsddb.Client
	item         map[string]types.AttributeValue
	writes       []types.TransactWriteItem
	fail         bool
	lostResponse bool
}

func (d *approvalDB) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: d.item}, nil
}
func (d *approvalDB) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	d.writes = in.TransactItems
	if d.fail {
		return nil, errors.New("conditional conflict")
	}
	d.item = in.TransactItems[0].Put.Item
	if d.lostResponse {
		return nil, errors.New("response lost after commit")
	}
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

type approvalDocuments struct {
	DocumentStore
	calls int
	fail  bool
}

func (d *approvalDocuments) PutPDF(context.Context, string, []byte) error {
	d.calls++
	if d.fail {
		return errors.New("S3 unavailable")
	}
	return nil
}

func approvalFixture(t *testing.T) (*App, events.APIGatewayV2HTTPRequest, *approvalDB, *approvalDocuments) {
	t.Helper()
	content := domain.Content{
		Representatives:       []domain.Person{{Name: "Representante", DNI: "16915292-6"}},
		Institution:           domain.Institution{Name: "Colegio", Address: "Direccion", Course: "4 medio"},
		ClientRepresentatives: []domain.Person{{Name: "Apoderado", DNI: "12345678-5", Course: "4 medio"}},
		Trip:                  domain.Trip{City: "Santiago", ContractDate: "2026-09-30", Destination: "Sur", Days: 5, Nights: 4, DeparturePoint: "Colegio"},
		Plan:                  domain.Plan{Name: "Gira", ServicesIncluded: []domain.Service{{Description: "Transporte"}}},
		Payments:              domain.Payments{PricePerPerson: 100000, DownPayment: 10000, DaysBeforePayment: 10, MaxExchangeRate: 1100, Installments: domain.Installments{Quantity: 5, StartMonth: "01", StartYear: 2027, StartDay: 31}, BankAccount: domain.BankAccount{AccountNumber: "1234", AccountHolder: "Operador", HolderDNI: "16915292-6", Bank: "Banco", Email: "demo@example.test"}},
		Passengers:            []domain.Passenger{{Names: "Ana", LastNames: "Prueba", DNI: "12345678-5", BirthDate: "2010-01-01", Nationality: "Chile", Sex: "FEMALE"}},
	}
	ref := &domain.ProgramReference{ID: "program", Name: "Gira", UpdatedAt: "2026-09-30T00:00:00Z"}
	item, err := domain.NewItem("01ARZ3NDEKTSV4RRFFQ69G5FAV", "program", ref, "2027-01", content, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	item.Status = domain.StatusPendingApproval
	raw, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatal(err)
	}
	db := &approvalDB{item: raw}
	documents := &approvalDocuments{}
	app := &App{Config: Config{ProgramsTableName: "contracts", PaymentsTableName: "payments"}, DDB: db, DDBTransactions: db, Documents: documents, PaymentAccess: func(context.Context) (PaymentAccessConfig, error) { return accessFixture(), nil }}
	content.PaymentPortal = &domain.PaymentPortal{URL: "https://attacker.example", TripCode: "FORGED"}
	body, err := json.Marshal(UpdateRequest{Content: content, ProgramID: "program", ProgramReference: ref, Period: "2027-01", Version: item.Version, Status: domain.StatusApproved})
	if err != nil {
		t.Fatal(err)
	}
	req := events.APIGatewayV2HTTPRequest{Body: string(body), PathParameters: map[string]string{ContractIDParam: item.ID}, RequestContext: events.APIGatewayV2HTTPRequestContext{Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]interface{}{"userId": "operator"}}}}
	return app, req, db, documents
}

func TestApprovalCommitsContractAuditCodeAndPendingPlanTogether(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "lost response"}[lost], func(t *testing.T) {
			app, req, db, documents := approvalFixture(t)
			db.lostResponse = lost
			response, err := updateContract(context.Background(), req, app)
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("approval: %d %s %v", response.StatusCode, response.Body, err)
			}
			if len(db.writes) != 4 || documents.calls != 1 {
				t.Fatal("approval is not one transaction with four writes")
			}
			var item domain.Item
			if err = attributevalue.UnmarshalMap(db.item, &item); err != nil {
				t.Fatal(err)
			}
			if item.Content.PaymentPortal == nil || item.Content.PaymentPortal.TripCode == "FORGED" || item.Content.PaymentPortal.URL != accessFixture().URL {
				t.Fatal("client controlled payment access")
			}
			if item.PDFDocument == nil || item.ApprovedBy != "operator" {
				t.Fatal("missing approved document audit")
			}
		})
	}
}

func TestApprovalFailuresLeaveContractUnapproved(t *testing.T) {
	for _, scenario := range []string{"config", "S3", "transaction"} {
		t.Run(scenario, func(t *testing.T) {
			app, req, db, documents := approvalFixture(t)
			switch scenario {
			case "config":
				app.PaymentAccess = func(context.Context) (PaymentAccessConfig, error) {
					return PaymentAccessConfig{}, errors.New("missing")
				}
			case "S3":
				documents.fail = true
			case "transaction":
				db.fail = true
			}
			response, err := updateContract(context.Background(), req, app)
			if err != nil || response.StatusCode < 400 {
				t.Fatal("failure approved contract")
			}
			var item domain.Item
			if err = attributevalue.UnmarshalMap(db.item, &item); err != nil {
				t.Fatal(err)
			}
			if item.Status != domain.StatusPendingApproval || item.Content.PaymentPortal != nil {
				t.Fatal("partial approval persisted")
			}
			if scenario == "config" && documents.calls != 0 {
				t.Fatal("generated PDF without valid configuration")
			}
		})
	}
}
