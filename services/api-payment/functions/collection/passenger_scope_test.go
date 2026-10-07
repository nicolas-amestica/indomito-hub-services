package collection

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestPassengerScopeRequiresVerifiedContextAndLiveCode(t *testing.T) {
	for _, scenario := range []string{"valid", "no authorizer", "admin", "revoked", "wrong trip", "unpublished", "forged body"} {
		t.Run(scenario, func(t *testing.T) {
			s, db := serviceFixture(t)
			id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
			other := "01ARZ3NDEKTSV4RRFFQ69G5FAW"
			code := "CODE#" + strings.Repeat("a", 64)
			a, err := s.GetAccount(context.Background(), "account")
			if err != nil {
				t.Fatal(err)
			}
			a.ID = id
			a.TripID = id
			for _, row := range []record{{PK: code, SK: "META", TripID: id, Status: "RESERVED_APPROVED"}, {PK: "TRIP#" + id, SK: "META", Status: "ACTIVE"}, {PK: "ACCOUNT#" + id, SK: "META", Version: a.Version, Account: &a}} {
				saveLookupRow(t, db, row)
			}
			req := events.APIGatewayV2HTTPRequest{RequestContext: events.APIGatewayV2HTTPRequestContext{Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]interface{}{"paymentAccess": "passenger", "paymentAccountId": id, "paymentTripId": id, "paymentSessionId": id, "paymentCodeKey": code}}}}
			switch scenario {
			case "no authorizer":
				req.RequestContext.Authorizer = nil
			case "admin":
				req.RequestContext.Authorizer.Lambda["paymentAccess"] = "admin"
			case "revoked":
				saveLookupRow(t, db, record{PK: code, SK: "META", TripID: id, Status: "REVOKED"})
			case "wrong trip":
				req.RequestContext.Authorizer.Lambda["paymentTripId"] = other
			case "unpublished":
				saveLookupRow(t, db, record{PK: "TRIP#" + id, SK: "META", Status: "PREPARING"})
			case "forged body":
				req.RequestContext.Authorizer = nil
				req.Body = `{"paymentAccess":"passenger","paymentAccountId":"` + id + `"}`
			}
			account, err := s.PassengerAccount(context.Background(), req)
			if scenario == "valid" {
				if err != nil || account.ID != id {
					t.Fatalf("valid denied %v", err)
				}
			} else if err == nil {
				t.Fatal("unauthorized scope accepted")
			}
		})
	}
}
