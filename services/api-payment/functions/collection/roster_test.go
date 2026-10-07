package collection

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

func TestListRosterUsesTripPartitionAndPaginates(t *testing.T) {
	db := &transactionDB{items: map[string]map[string]types.AttributeValue{}}
	s := Service{DB: db, Table: "payments"}
	input := domain.Startup{TripID: approvedID, ContractID: approvedID, ContractVersion: 2, Approved: true, PricePerPayer: 100000, DueDates: []string{"2027-01-05"}}
	for i := 0; i < 55; i++ {
		id := paymentOperationID(fmt.Sprintf("person-%03d", i))
		input.Participants = append(input.Participants, domain.Participant{ID: id, Name: fmt.Sprintf("Pasajero %03d", i), Document: fmt.Sprintf("DOC-%03d", i)})
	}
	if err := s.PreparePlan(context.Background(), input, "operator"); err != nil {
		t.Fatal(err)
	}
	first, err := s.ListRoster(context.Background(), approvedID, "")
	if err != nil || len(first.Items) != 50 || first.NextCursor == "" {
		t.Fatalf("wrong first page: %+v %v", first, err)
	}
	second, err := s.ListRoster(context.Background(), approvedID, first.NextCursor)
	if err != nil || len(second.Items) != 5 || second.NextCursor != "" {
		t.Fatalf("wrong second page: %+v %v", second, err)
	}
}
