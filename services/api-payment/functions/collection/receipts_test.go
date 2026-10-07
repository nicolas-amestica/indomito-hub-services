package collection

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestReceiptWorkerDynamoDBWireContract(t *testing.T) {
	// Node deserializa el documento DynamoDB, no la representación JSON pública.
	event := domain.Event{Audit: domain.Audit{RecordedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}, AccountID: approvedID, TripID: approvedID, Amount: 20000, Type: "PAYMENT_RECEIVED", EffectiveDate: "2026-10-01"}
	item, err := attributevalue.MarshalMap(record{PK: "RECEIPT#" + approvedID, SK: "META", ReceiptID: approvedID, ReceiptEmail: "payer@example.test", Event: &event})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = attributevalue.UnmarshalMap(item, &document); err != nil {
		t.Fatal(err)
	}
	wire, ok := document["event"].(map[string]any)
	if !ok || wire["RecordedAt"] != "2026-10-01T12:00:00Z" || wire["AccountID"] != approvedID || wire["Amount"] != float64(20000) || document["receiptId"] != approvedID || document["receiptEmail"] != "payer@example.test" {
		t.Fatalf("incompatible receipt document: %#v", document)
	}
}

func (d *transactionDB) Query(ctx context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	pk := in.ExpressionAttributeValues[":pk"].(*types.AttributeValueMemberS).Value
	prefix := ""
	from, to := "", ""
	if value, ok := in.ExpressionAttributeValues[":prefix"].(*types.AttributeValueMemberS); ok {
		prefix = value.Value
	} else {
		from = in.ExpressionAttributeValues[":from"].(*types.AttributeValueMemberS).Value
		to = in.ExpressionAttributeValues[":to"].(*types.AttributeValueMemberS).Value
	}
	start := ""
	if value, ok := in.ExclusiveStartKey["sk"].(*types.AttributeValueMemberS); ok {
		start = value.Value
	}
	keys := []string{}
	for k, row := range d.items {
		sk := row["sk"].(*types.AttributeValueMemberS).Value
		matchesRange := (prefix != "" && strings.HasPrefix(sk, prefix)) || (prefix == "" && sk >= from && sk <= to)
		if row["pk"].(*types.AttributeValueMemberS).Value == pk && matchesRange && sk > start {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := &dynamodb.QueryOutput{}
	for index, k := range keys {
		if index >= int(*in.Limit) {
			last := d.items[keys[index-1]]
			out.LastEvaluatedKey = key(pk, last["sk"].(*types.AttributeValueMemberS).Value)
			break
		}
		out.Items = append(out.Items, d.items[k])
	}
	return out, nil
}

type receiptSignerFake struct{ calls int }

func (s *receiptSignerFake) Download(context.Context, string) (string, error) {
	s.calls++
	return "https://private.s3.us-east-1.amazonaws.com/receipt.pdf?signature=test", nil
}

func TestReceiptDownloadRequiresOwnerAndPrivateDocument(t *testing.T) {
	for _, scenario := range []string{"ready", "foreign", "pending", "tampered-key"} {
		t.Run(scenario, func(t *testing.T) {
			p, db, _, req := portalFixture(t)
			signer := &receiptSignerFake{}
			a := ReceiptsApp{Accounts: p.Accounts, Signer: signer}
			req.RequestContext.HTTP.Method = "GET"
			req.RequestContext.Authorizer.Lambda = map[string]any{"userId": "operator", "paymentAccess": "admin"}
			req.PathParameters = map[string]string{"accountId": approvedID, "id": approvedID}
			row := record{PK: "RECEIPT#" + approvedID, SK: "META", ReceiptID: approvedID, Event: &domain.Event{AccountID: approvedID}, DocumentKey: "receipts/" + approvedID + "/" + approvedID + "/v1.pdf", DocumentSHA256: strings.Repeat("a", 64)}
			switch scenario {
			case "foreign":
				row.Event.AccountID = "other"
			case "pending":
				row.DocumentKey = ""
			case "tampered-key":
				row.DocumentKey = "contracts/private.pdf"
			}
			saveLookupRow(t, db, row)
			response := a.HandleDownload(context.Background(), req)
			if scenario == "ready" {
				if response.StatusCode != 200 || signer.calls != 1 {
					t.Fatal("owner blocked")
				}
			} else if response.StatusCode < 400 || signer.calls != 0 {
				t.Fatal("unsafe document disclosed")
			}
		})
	}
}

func TestReceiptListOnlyReturnsAuthorizedAccount(t *testing.T) {
	p, db, _, req := portalFixture(t)
	a := ReceiptsApp{Accounts: p.Accounts}
	req.RequestContext.HTTP.Method = "GET"
	req.RequestContext.Authorizer.Lambda = map[string]any{"userId": "operator", "paymentAccess": "admin"}
	req.PathParameters = map[string]string{"accountId": approvedID}
	saveLookupRow(t, db, record{PK: "ACCOUNT#" + approvedID, SK: "RECEIPT#" + approvedID, AccountID: approvedID, ReceiptID: approvedID, Event: &domain.Event{Amount: 20000, EffectiveDate: "2026-10-01"}})
	saveLookupRow(t, db, record{PK: "ACCOUNT#other", SK: "RECEIPT#" + approvedID, AccountID: "other", ReceiptID: approvedID, Event: &domain.Event{Amount: 99000, EffectiveDate: "2026-10-01"}})
	response := a.HandleList(context.Background(), req)
	var body struct {
		Data ReceiptPage `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || len(body.Data.Items) != 1 || body.Data.Items[0].Amount != 20000 {
		t.Fatalf("list %s", response.Body)
	}
	req.QueryStringParameters = map[string]string{"cursor": "../../other"}
	if a.HandleList(context.Background(), req).StatusCode != 400 {
		t.Fatal("invalid cursor accepted")
	}
}
