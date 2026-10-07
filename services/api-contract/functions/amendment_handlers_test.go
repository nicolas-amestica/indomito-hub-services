package functions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/services/api-contract/domain"
)

type amendmentDB struct {
	awsddb.Client
	items   map[string]map[string]types.AttributeValue
	commits int
}

func amendmentItemKey(item map[string]types.AttributeValue) string {
	pk, _ := item["pk"].(*types.AttributeValueMemberS)
	sk, _ := item["sk"].(*types.AttributeValueMemberS)
	if pk == nil || sk == nil {
		return ""
	}
	return pk.Value + "/" + sk.Value
}

func (d *amendmentDB) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: d.items[amendmentItemKey(in.Key)]}, nil
}

func (d *amendmentDB) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	pk := in.ExpressionAttributeValues[":pk"].(*types.AttributeValueMemberS).Value
	prefix := in.ExpressionAttributeValues[":prefix"].(*types.AttributeValueMemberS).Value
	keys := make([]string, 0)
	for key, item := range d.items {
		if strings.HasPrefix(key, pk+"/"+prefix) {
			keys = append(keys, key)
			_ = item
		}
	}
	sort.Strings(keys)
	out := &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{}}
	for _, key := range keys {
		out.Items = append(out.Items, d.items[key])
	}
	return out, nil
}

func (d *amendmentDB) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	for _, action := range in.TransactItems {
		if action.ConditionCheck != nil {
			item := d.items[amendmentItemKey(action.ConditionCheck.Key)]
			if *action.ConditionCheck.ConditionExpression == "attribute_not_exists(pk)" {
				if item != nil {
					return nil, &types.TransactionCanceledException{}
				}
				continue
			}
			if *action.ConditionCheck.ConditionExpression == "revision = :revision" {
				revision, ok := item["revision"].(*types.AttributeValueMemberN)
				if !ok || revision.Value != action.ConditionCheck.ExpressionAttributeValues[":revision"].(*types.AttributeValueMemberN).Value {
					return nil, &types.TransactionCanceledException{}
				}
				continue
			}
			status, statusOK := item["status"].(*types.AttributeValueMemberS)
			version, versionOK := item["version"].(*types.AttributeValueMemberN)
			wantedVersion := action.ConditionCheck.ExpressionAttributeValues[":version"]
			if wantedVersion == nil {
				wantedVersion = action.ConditionCheck.ExpressionAttributeValues[":baseVersion"]
			}
			if !statusOK || !versionOK || status.Value != action.ConditionCheck.ExpressionAttributeValues[":approved"].(*types.AttributeValueMemberS).Value || version.Value != wantedVersion.(*types.AttributeValueMemberN).Value {
				return nil, &types.TransactionCanceledException{}
			}
		}
		if action.Put != nil {
			old := d.items[amendmentItemKey(action.Put.Item)]
			switch *action.Put.ConditionExpression {
			case "attribute_not_exists(pk)":
				if old != nil {
					return nil, &types.TransactionCanceledException{}
				}
			case "#status = :draft AND version = :version":
				status, statusOK := old["status"].(*types.AttributeValueMemberS)
				version, versionOK := old["version"].(*types.AttributeValueMemberN)
				if !statusOK || !versionOK || status.Value != action.Put.ExpressionAttributeValues[":draft"].(*types.AttributeValueMemberS).Value || version.Value != action.Put.ExpressionAttributeValues[":version"].(*types.AttributeValueMemberN).Value {
					return nil, &types.TransactionCanceledException{}
				}
			case "attribute_not_exists(pk) OR revision = :baseRevision":
				if old != nil {
					revision, ok := old["revision"].(*types.AttributeValueMemberN)
					if !ok || revision.Value != action.Put.ExpressionAttributeValues[":baseRevision"].(*types.AttributeValueMemberN).Value {
						return nil, &types.TransactionCanceledException{}
					}
				}
			default:
				return nil, errors.New("unexpected condition")
			}
		}
		if action.Delete != nil {
			old := d.items[amendmentItemKey(action.Delete.Key)]
			amendmentID, ok := old["amendmentId"].(*types.AttributeValueMemberS)
			if !ok || amendmentID.Value != action.Delete.ExpressionAttributeValues[":amendmentId"].(*types.AttributeValueMemberS).Value {
				return nil, &types.TransactionCanceledException{}
			}
		}
	}
	for _, action := range in.TransactItems {
		if action.Put != nil {
			d.items[amendmentItemKey(action.Put.Item)] = action.Put.Item
		}
		if action.Delete != nil {
			delete(d.items, amendmentItemKey(action.Delete.Key))
		}
	}
	d.commits++
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

type amendmentDocuments struct {
	objects map[string][]byte
}

func (d *amendmentDocuments) PutPDF(_ context.Context, key string, content []byte) error {
	d.objects[key] = append([]byte(nil), content...)
	return nil
}

func (d *amendmentDocuments) VerifyPDF(_ context.Context, key, expectedSHA string, expectedSize int64) error {
	content := d.objects[key]
	digest := sha256.Sum256(content)
	if int64(len(content)) != expectedSize || hex.EncodeToString(digest[:]) != expectedSHA {
		return errors.New("integrity mismatch")
	}
	return nil
}

func (d *amendmentDocuments) PresignPDF(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://documents.example/" + key, nil
}

func amendmentFixture(t *testing.T) (*App, domain.Item, *amendmentDB, *amendmentDocuments) {
	t.Helper()
	_, _, approval, _ := approvalFixture(t)
	var contract domain.Item
	if err := attributevalue.UnmarshalMap(approval.item, &contract); err != nil {
		t.Fatal(err)
	}
	contract.Status = domain.StatusApproved
	contract.Version = 4
	raw, err := attributevalue.MarshalMap(contract)
	if err != nil {
		t.Fatal(err)
	}
	db := &amendmentDB{items: map[string]map[string]types.AttributeValue{amendmentItemKey(raw): raw}}
	documents := &amendmentDocuments{objects: map[string][]byte{}}
	return &App{Config: Config{ProgramsTableName: "contracts", PaymentsTableName: "payments"}, DDB: db, DDBTransactions: db, Documents: documents}, contract, db, documents
}

func amendmentRequest(contractID string, body any) events.APIGatewayV2HTTPRequest {
	raw, _ := json.Marshal(body)
	return events.APIGatewayV2HTTPRequest{Body: string(raw), PathParameters: map[string]string{ContractIDParam: contractID}, RequestContext: events.APIGatewayV2HTTPRequestContext{Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]any{"userId": "operator"}}}}
}

func TestAmendmentCreateApproveListAndPDFAreIdempotent(t *testing.T) {
	app, contract, db, documents := amendmentFixture(t)
	after := domain.TermsSnapshot(contract.Content)
	after.DepartureDate, after.ReturnDate, after.Days, after.Nights = "2027-10-05", "2027-10-08", 4, 3
	after.Services = append(after.Services, domain.Service{Description: "Excursión adicional"})
	id := domain.NewID()
	request := amendmentRequest(contract.ID, CreateAmendmentRequest{ID: id, BaseContractVersion: contract.Version, Reason: "Definir fecha y servicio adicional", After: after})
	response, err := createAmendment(context.Background(), request, app)
	if err != nil || response.StatusCode != 201 {
		t.Fatalf("create: %+v %v", response, err)
	}
	commits := db.commits
	response, err = createAmendment(context.Background(), request, app)
	if err != nil || response.StatusCode != 200 || db.commits != commits {
		t.Fatalf("create replay: %+v %v commits=%d", response, err, db.commits)
	}
	other := amendmentRequest(contract.ID, CreateAmendmentRequest{ID: domain.NewID(), BaseContractVersion: contract.Version, Reason: "Otro borrador en paralelo", After: after})
	response, err = createAmendment(context.Background(), other, app)
	if err != nil || response.StatusCode != 409 || db.commits != commits {
		t.Fatalf("parallel draft accepted: %+v %v", response, err)
	}
	approve := amendmentRequest(contract.ID, ApproveAmendmentRequest{Version: 1})
	approve.PathParameters[AmendmentIDParam] = id
	response, err = approveAmendment(context.Background(), approve, app)
	if err != nil || response.StatusCode != 200 || len(documents.objects) != 1 {
		t.Fatalf("approve: %+v %v", response, err)
	}
	projection := db.items["TRIP#"+contract.ID+"/TERMS#CURRENT"]
	if projection == nil || projection["revision"].(*types.AttributeValueMemberN).Value != "1" {
		t.Fatalf("payment terms projection missing: %+v", projection)
	}
	commits = db.commits
	response, err = approveAmendment(context.Background(), approve, app)
	if err != nil || response.StatusCode != 200 || db.commits != commits || len(documents.objects) != 1 {
		t.Fatalf("approve replay: %+v %v commits=%d", response, err, db.commits)
	}
	list := amendmentRequest(contract.ID, nil)
	response, err = listAmendments(context.Background(), list, app)
	if err != nil || response.StatusCode != 200 || !strings.Contains(response.Body, id) || !strings.Contains(response.Body, "APPROVED") {
		t.Fatalf("list: %+v %v", response, err)
	}
	pdfRequest := amendmentRequest(contract.ID, nil)
	pdfRequest.PathParameters[AmendmentIDParam] = id
	response, err = approvedAmendmentPDF(context.Background(), pdfRequest, app)
	if err != nil || response.StatusCode != 200 || !strings.Contains(response.Body, "https://documents.example/") {
		t.Fatalf("pdf: %+v %v", response, err)
	}
	secondAfter := after
	secondAfter.Services = append(append([]domain.Service(nil), after.Services...), domain.Service{Description: "Hotel actualizado"})
	secondID := domain.NewID()
	second := amendmentRequest(contract.ID, CreateAmendmentRequest{ID: secondID, BaseContractVersion: contract.Version, Reason: "Actualizar alojamiento contratado", After: secondAfter})
	response, err = createAmendment(context.Background(), second, app)
	if err != nil || response.StatusCode != 201 {
		t.Fatalf("second create: %+v %v", response, err)
	}
	persisted, _, readErr := getAmendment(context.Background(), app, contract.ID, secondID)
	if readErr != nil || persisted.BaseTermsRevision != 1 || persisted.Before.DepartureDate != "2027-10-05T00:00:00Z" || len(persisted.Before.Services) != len(after.Services) {
		t.Fatalf("second amendment ignored effective terms: %+v %v", persisted, readErr)
	}
}
