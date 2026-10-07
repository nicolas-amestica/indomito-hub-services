package collection

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestCollectionAlertsDoNotInventCutoffWithoutTravelDate(t *testing.T) {
	s, _, input, _ := groupDepositFixture(t)
	req := adminRequest("GET", "")
	req.PathParameters = map[string]string{"tripId": input.TripID}
	req.QueryStringParameters = map[string]string{"asOf": "2027-02-01"}
	response, err := (TreasuryAdminApp{Accounts: s, Now: time.Now}).HandleAlerts(context.Background(), req)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	var envelope struct {
		Data CollectionAlertView `json:"data"`
	}
	if json.Unmarshal([]byte(response.Body), &envelope) != nil {
		t.Fatal(response.Body)
	}
	if envelope.Data.TravelDateDefined || envelope.Data.CutoffDate != "" || envelope.Data.Overdue != 2700 || len(envelope.Data.Accounts) != 3 {
		t.Fatalf("unexpected alert: %+v", envelope.Data)
	}
}

func TestCollectionAlertsUseEffectiveAmendmentTravelDate(t *testing.T) {
	s, db, input, _ := groupDepositFixture(t)
	saveLookupRow(t, db, record{PK: "TRIP#" + input.TripID, SK: "TERMS#CURRENT", Terms: &PaymentTerms{DepartureDate: "2027-02-20T12:00:00Z"}})
	req := adminRequest("GET", "")
	req.PathParameters = map[string]string{"tripId": input.TripID}
	req.QueryStringParameters = map[string]string{"asOf": "2027-01-01"}
	response, _ := (TreasuryAdminApp{Accounts: s, Now: time.Now}).HandleAlerts(context.Background(), req)
	var envelope struct {
		Data CollectionAlertView `json:"data"`
	}
	if json.Unmarshal([]byte(response.Body), &envelope) != nil {
		t.Fatal(response.Body)
	}
	if !envelope.Data.TravelDateDefined || envelope.Data.DepartureDate != "2027-02-20" || envelope.Data.CutoffDate != "2027-02-20" || envelope.Data.DueByCutoff != 2700 {
		t.Fatalf("effective terms ignored: %+v", envelope.Data)
	}
}
