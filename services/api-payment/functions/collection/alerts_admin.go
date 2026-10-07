package collection

import (
	"context"
	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"time"
)

type DelinquentAccount struct {
	AccountID   string `json:"accountId"`
	Name        string `json:"name"`
	Overdue     int64  `json:"overdue"`
	Outstanding int64  `json:"outstanding"`
	NextDueDate string `json:"nextDueDate,omitempty"`
}
type CollectionAlertView struct {
	AsOf              string              `json:"asOf"`
	DepartureDate     string              `json:"departureDate,omitempty"`
	CutoffDate        string              `json:"cutoffDate,omitempty"`
	TravelDateDefined bool                `json:"travelDateDefined"`
	Overdue           int64               `json:"overdue"`
	Outstanding       int64               `json:"outstanding"`
	DueByCutoff       int64               `json:"dueByCutoff"`
	Accounts          []DelinquentAccount `json:"accounts"`
}

func (a TreasuryAdminApp) HandleAlerts(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	tripID, asOf := req.PathParameters["tripId"], req.QueryStringParameters["asOf"]
	if req.RequestContext.HTTP.Method != "GET" || !validPortalID(tripID) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if asOf == "" && a.Now != nil {
		zone, _ := time.LoadLocation("America/Santiago")
		asOf = a.Now().In(zone).Format(time.DateOnly)
	}
	if _, err := time.Parse(time.DateOnly, asOf); err != nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	view, err := a.buildCollectionAlert(ctx, tripID, asOf)
	if err != nil {
		return domainFailure(req, err)
	}
	return lambdautil.SuccessResponseWithHeaders(200, view, map[string]string{"cache-control": "no-store"})
}

func (a TreasuryAdminApp) buildCollectionAlert(ctx context.Context, tripID, asOf string) (CollectionAlertView, error) {
	plan, err := a.Accounts.read(ctx, "TRIP#"+tripID, "SETUP")
	if err != nil {
		return CollectionAlertView{}, err
	}
	if plan.Startup == nil {
		return CollectionAlertView{}, domain.ErrConflict
	}
	departure := plan.Startup.DepartureDate
	if terms, termsErr := a.Accounts.read(ctx, "TRIP#"+tripID, "TERMS#CURRENT"); termsErr == nil && terms.Terms != nil {
		raw := terms.Terms.DepartureDate
		if parsed, e := time.Parse(time.RFC3339Nano, raw); e == nil {
			departure = parsed.UTC().Format(time.DateOnly)
		} else if raw == "" {
			departure = ""
		}
	}
	view := CollectionAlertView{AsOf: asOf, DepartureDate: departure, TravelDateDefined: departure != "", Accounts: []DelinquentAccount{}}
	if departure != "" {
		d, _ := time.Parse(time.DateOnly, departure)
		view.CutoffDate = d.AddDate(0, 0, -plan.Startup.DaysBeforeDeparture).Format(time.DateOnly)
	}
	cursor := ""
	for {
		roster, e := a.Accounts.ListRoster(ctx, tripID, cursor)
		if e != nil {
			return CollectionAlertView{}, e
		}
		for _, member := range roster.Items {
			account, e := a.Accounts.GetAccount(ctx, member.AccountID)
			if e != nil {
				return CollectionAlertView{}, e
			}
			row := DelinquentAccount{AccountID: account.ID, Name: member.Name}
			for _, installment := range account.Installments {
				amount := installment.Outstanding()
				if amount <= 0 {
					continue
				}
				row.Outstanding += amount
				if row.NextDueDate == "" {
					row.NextDueDate = installment.DueDate
				}
				if installment.DueDate < asOf {
					row.Overdue += amount
				}
				if view.CutoffDate != "" && installment.DueDate <= view.CutoffDate {
					view.DueByCutoff += amount
				}
			}
			view.Outstanding += row.Outstanding
			view.Overdue += row.Overdue
			if row.Overdue > 0 {
				view.Accounts = append(view.Accounts, row)
			}
		}
		if roster.NextCursor == "" {
			break
		}
		cursor = roster.NextCursor
	}
	return view, nil
}

func TreasuryAlertsHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (TreasuryAdminApp{Accounts: app.Accounts, Now: time.Now}).HandleAlerts(ctx, req)
}
