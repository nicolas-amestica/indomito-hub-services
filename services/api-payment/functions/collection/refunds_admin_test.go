package collection

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
)

func TestRefundDashboardListsWithdrawalWithoutTreatingDepositAsRefundable(t *testing.T) {
	s, db, input, ids := groupDepositFixture(t)
	account, err := s.GetAccount(context.Background(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	account.Active = false
	account.Installments[0].Paid = 100
	account.Installments[0].Cancelled = account.Installments[0].Original - 100
	account.Version++
	raw, err := attributevalue.MarshalMap(record{PK: "ACCOUNT#" + account.ID, SK: "META", Version: account.Version, Account: &account})
	if err != nil {
		t.Fatal(err)
	}
	db.items[itemKey(raw)] = raw
	req := adminRequest("GET", "")
	req.PathParameters = map[string]string{"tripId": input.TripID}
	response, err := (TreasuryAdminApp{Accounts: s}).HandleRefunds(context.Background(), req)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	var envelope struct {
		Data RefundPage `json:"data"`
	}
	if json.Unmarshal([]byte(response.Body), &envelope) != nil || len(envelope.Data.Items) != 1 {
		t.Fatalf("body=%s", response.Body)
	}
	row := envelope.Data.Items[0]
	if row.PaidInstallments != 100 || row.WithdrawalRefundApproved != 0 || row.RefundPayable != 0 {
		t.Fatalf("deposit or unapproved refund leaked into payable: %+v", row)
	}
}
