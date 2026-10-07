package collection

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
)

type contractAlertSummary struct {
	ID              string `dynamodbav:"id"`
	Status          string `dynamodbav:"status"`
	InstitutionName string `dynamodbav:"institutionName"`
	Destination     string `dynamodbav:"destination"`
}

type GroupCollectionAlert struct {
	TripID          string `json:"tripId"`
	InstitutionName string `json:"institutionName"`
	Destination     string `json:"destination"`
	CollectionAlertView
}

type GlobalCollectionAlerts struct {
	Year        string                 `json:"year"`
	AsOf        string                 `json:"asOf"`
	Overdue     int64                  `json:"overdue"`
	Outstanding int64                  `json:"outstanding"`
	Groups      []GroupCollectionAlert `json:"groups"`
}

func GlobalCollectionAlertsHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return handleGlobalCollectionAlerts(ctx, req, app)
}

func handleGlobalCollectionAlerts(ctx context.Context, req events.APIGatewayV2HTTPRequest, app *AdminApp) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	year, asOf := strings.TrimSpace(req.QueryStringParameters["year"]), strings.TrimSpace(req.QueryStringParameters["asOf"])
	if req.RequestContext.HTTP.Method != "GET" || len(year) != 4 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if asOf == "" {
		zone, _ := time.LoadLocation("America/Santiago")
		asOf = time.Now().In(zone).Format(time.DateOnly)
	}
	if _, err := time.Parse(time.DateOnly, asOf); err != nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	db, ok := app.Contracts.DB.(queryDatabase)
	if !ok {
		return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
	}
	contracts := []contractAlertSummary{}
	var cursor map[string]types.AttributeValue
	for {
		out, queryErr := db.Query(ctx, &dynamodb.QueryInput{TableName: &app.Contracts.Table, IndexName: aws.String("gsi-periodo-documental-index"), KeyConditionExpression: aws.String("gsiPeriodPk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "CONTRACT#YEAR#" + year}}, ProjectionExpression: aws.String("id, #status, institutionName, destination"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ConsistentRead: aws.Bool(false), Limit: aws.Int32(50), ExclusiveStartKey: cursor})
		if queryErr != nil {
			return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
		}
		for _, raw := range out.Items {
			var item contractAlertSummary
			if attributevalue.UnmarshalMap(raw, &item) == nil && item.Status == "APPROVED" && validPortalID(item.ID) {
				contracts = append(contracts, item)
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		cursor = out.LastEvaluatedKey
	}
	result := GlobalCollectionAlerts{Year: year, AsOf: asOf, Groups: []GroupCollectionAlert{}}
	treasury := TreasuryAdminApp{Accounts: app.Accounts}
	for _, contract := range contracts {
		view, buildErr := treasury.buildCollectionAlert(ctx, contract.ID, asOf)
		if errors.Is(buildErr, ErrNotFound) {
			continue
		}
		if buildErr != nil {
			return domainFailure(req, buildErr)
		}
		if view.Outstanding == 0 {
			continue
		}
		result.Overdue += view.Overdue
		result.Outstanding += view.Outstanding
		result.Groups = append(result.Groups, GroupCollectionAlert{TripID: contract.ID, InstitutionName: contract.InstitutionName, Destination: contract.Destination, CollectionAlertView: view})
	}
	return lambdautil.SuccessResponseWithHeaders(200, result, map[string]string{"cache-control": "no-store"})
}
