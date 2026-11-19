package functions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	budget "ind-hub-api-gox-sls-pri-gh/services/api-analytics/domain/analytics"
	"regexp"
	"strings"
	"time"
)

var analyticsYear = regexp.MustCompile(`^20[0-9]{2}$`)

// AnalyticsHandler autoriza antes de inicializar AWS y no confía en permisos enviados en el cuerpo.
func AnalyticsHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		return adminFailure(req, 401, "UNAUTHORIZED")
	}
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
	}
	return app.handleAnalytics(ctx, req, actor, time.Now())
}

func (a AdminApp) handleAnalytics(ctx context.Context, req events.APIGatewayV2HTTPRequest, actor string, now time.Time) (events.APIGatewayV2HTTPResponse, error) {
	id := req.PathParameters["id-viaje"]
	method := req.RequestContext.HTTP.Method
	if id != "" && !validPortalID(id) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if method == "PUT" {
		if id == "" || len(req.Body) > 190000 {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		var command budget.Command
		if decodeAdmin(req, &command) != nil {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		if _, err := a.analyticsTrip(ctx, id); err != nil {
			return domainFailure(req, err)
		}
		state, err := a.Accounts.SaveAnalytics(ctx, id, command, actor, now)
		if err != nil {
			return domainFailure(req, err)
		}
		return lambdautil.SuccessResponseWithHeaders(200, state, map[string]string{"cache-control": "no-store"})
	}
	if method != "GET" {
		return adminFailure(req, 400, "METHOD_NOT_ALLOWED")
	}
	from, to := req.QueryStringParameters["from"], req.QueryStringParameters["to"]
	zone, err := time.LoadLocation("America/Santiago")
	if err != nil {
		return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
	}
	today := now.In(zone).Format(time.DateOnly)
	if from == "" {
		from = today[:4] + "-01-01"
	}
	if to == "" {
		to = today
	}
	d1, e1 := time.Parse(time.DateOnly, from)
	d2, e2 := time.Parse(time.DateOnly, to)
	if e1 != nil || e2 != nil || from > to || len(monthKeys(d1, d2)) > 12 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if id != "" {
		trip, err := a.analyticsTrip(ctx, id)
		if err != nil {
			return domainFailure(req, err)
		}
		row, err := a.analyticsRow(ctx, trip, from, to)
		if err != nil {
			return domainFailure(req, err)
		}
		if v := req.QueryStringParameters["version"]; v != "" {
			var n int64
			if _, err = fmt.Sscan(v, &n); err != nil || n < 1 {
				return adminFailure(req, 400, "INVALID_REQUEST")
			}
			row.State, _, err = a.Accounts.analyticsRead(ctx, id, fmt.Sprintf("ANALYTICS#REV#%020d", n))
			if err != nil {
				return domainFailure(req, err)
			}
		}
		revisions, err := a.Accounts.analyticsRevisions(ctx, id)
		if err != nil {
			return domainFailure(req, err)
		}
		return lambdautil.SuccessResponseWithHeaders(200, map[string]interface{}{"row": row, "revisions": revisions, "from": from, "to": to, "asOf": today, "updatedAt": now.UTC().Format(time.RFC3339)}, map[string]string{"cache-control": "no-store"})
	}
	year, cursor := req.QueryStringParameters["year"], req.QueryStringParameters["cursor"]
	if len(cursor) > 1500 || !analyticsYear.MatchString(year) || len(cursor) > 2000 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	db, ok := a.Contracts.DB.(queryDatabase)
	if !ok {
		return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
	}
	input := &dynamodb.QueryInput{TableName: &a.Contracts.Table, IndexName: aws.String("gsi-periodo-documental-index"), KeyConditionExpression: aws.String("gsiPeriodPk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "CONTRACT#YEAR#" + year}}, Limit: aws.Int32(5)}
	if cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		var values map[string]string
		if json.Unmarshal(raw, &values) != nil || len(values) != 4 || !validPortalID(strings.TrimPrefix(values["pk"], "CONTRACT#")) || values["gsiPeriodPk"] != "CONTRACT#YEAR#"+year || values["sk"] != "METADATA" {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		input.ExclusiveStartKey = map[string]types.AttributeValue{}
		for k, v := range values {
			if k != "pk" && k != "sk" && k != "gsiPeriodPk" && k != "gsiPeriodSk" {
				return adminFailure(req, 400, "INVALID_REQUEST")
			}
			input.ExclusiveStartKey[k] = &types.AttributeValueMemberS{Value: v}
		}
	}
	rows := []AnalyticsRow{}
	out, err := db.Query(ctx, input)
	if err != nil {
		return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
	}
	for _, raw := range out.Items {
		var summary contractAlertSummary
		if attributevalue.UnmarshalMap(raw, &summary) != nil {
			return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
		}
		if summary.Status != "APPROVED" {
			continue
		}
		trip, err := a.analyticsTrip(ctx, summary.ID)
		if err != nil {
			return domainFailure(req, err)
		}
		row, err := a.analyticsRow(ctx, trip, from, to)
		if err != nil {
			return domainFailure(req, err)
		}
		row.Trip.ProgramReference = nil
		row.State.Scenarios = map[string]budget.Budget{}
		rows = append(rows, row)
	}
	nextCursor := ""
	if len(out.LastEvaluatedKey) > 0 {
		values := map[string]string{}
		for k, v := range out.LastEvaluatedKey {
			sv, ok := v.(*types.AttributeValueMemberS)
			if !ok {
				return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
			}
			values[k] = sv.Value
		}
		raw, e := json.Marshal(values)
		if e != nil {
			return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
		}
		nextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return lambdautil.SuccessResponseWithHeaders(200, map[string]interface{}{"items": rows, "nextCursor": nextCursor, "from": from, "to": to, "asOf": today, "updatedAt": now.UTC().Format(time.RFC3339)}, map[string]string{"cache-control": "no-store"})
}

func (a AdminApp) analyticsRow(ctx context.Context, trip AnalyticsTrip, from, to string) (AnalyticsRow, error) {
	state, _, err := a.Accounts.analyticsRead(ctx, trip.ID, "ANALYTICS#CURRENT")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return AnalyticsRow{}, err
	}
	finance, issue, err := a.Accounts.analyticsFinance(ctx, trip.ID, from, to)
	status := "AVAILABLE"
	if finance == nil {
		status = "NOT_ENABLED"
	}
	if err != nil {
		status = "UNAVAILABLE"
		issue = "No fue posible consultar la posición operativa. Reintenta antes de usar estos resultados."
		finance = nil
	}
	return AnalyticsRow{FinanceStatus: status, Trip: trip, State: state, Finance: finance, Issue: issue}, nil
}
