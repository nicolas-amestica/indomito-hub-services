package collection

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
)

type SIISubmissionQuerier interface {
	QuerySubmission(context.Context, sii.Credentials, string) (sii.SubmissionStatus, error)
}

type TaxSubmissionStatusApp struct {
	Accounts Service
	Config   SIIConfigLoader
	SII      SIISubmissionQuerier
	Now      func() time.Time
}

func (a TaxSubmissionStatusApp) Run(ctx context.Context) error {
	db, ok := a.Accounts.DB.(queryDatabase)
	if !ok || a.Config == nil || a.SII == nil || a.Now == nil {
		return errors.New("invalid tax status dependencies")
	}
	out, err := db.Query(ctx, &dynamodb.QueryInput{TableName: &a.Accounts.Table, KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "TAX_QUEUE#SUBMITTED"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(20)})
	if err != nil {
		return err
	}
	credentials, err := a.Config.Load(ctx, a.Now().UTC())
	if err != nil {
		return err
	}
	var failures []error
	for _, item := range out.Items {
		var row record
		if attributevalue.UnmarshalMap(item, &row) != nil || row.TaxRequest == nil || row.TaxRequest.Status != taxStatusSubmitted || row.TaxRequest.SIIIssuance == nil || row.TaxRequest.SIIIssuance.TrackID == "" {
			continue
		}
		status, queryErr := a.SII.QuerySubmission(ctx, credentials, row.TaxRequest.SIIIssuance.TrackID)
		if queryErr != nil {
			failures = append(failures, queryErr)
			continue
		}
		if status.State != "EPR" && status.State != "RSC" && status.State != "RFR" && status.State != "RCT" {
			continue
		}
		if err = a.finish(ctx, row, status); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (a TaxSubmissionStatusApp) finish(ctx context.Context, queue record, status sii.SubmissionStatus) error {
	now := a.Now().UTC()
	request := *queue.TaxRequest
	request.SIIIssuance.ProcessedAt = &now
	request.SIIIssuance.Status = status.State
	request.SIIIssuance.Detail = status.Detail
	request.Status = taxStatusRejected
	if status.Approved() && status.Repairs > 0 {
		request.Status = taxStatusAcceptedRepair
	} else if status.Approved() {
		request.Status = taxStatusAccepted
	}
	canonical, err := a.Accounts.put(record{PK: "TAX_REQUEST#" + request.RequestID, SK: "META", TaxRequest: &request, Status: request.Status, AccountID: request.AccountID, TripID: request.TripID}, "#status = :submitted", map[string]types.AttributeValue{":submitted": &types.AttributeValueMemberS{Value: taxStatusSubmitted}})
	if err != nil {
		return err
	}
	recorded, err := a.Accounts.put(record{PK: "TAX_QUEUE#RECORDED", SK: queue.SK, TaxRequest: &request, Status: request.Status, AccountID: request.AccountID, TripID: request.TripID}, "#status = :submitted", map[string]types.AttributeValue{":submitted": &types.AttributeValueMemberS{Value: taxStatusSubmitted}})
	if err != nil {
		return err
	}
	deleteSubmitted := types.TransactWriteItem{Delete: &types.Delete{TableName: &a.Accounts.Table, Key: key(queue.PK, queue.SK), ConditionExpression: aws.String("#status = :submitted"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":submitted": &types.AttributeValueMemberS{Value: taxStatusSubmitted}}}}
	items := []types.TransactWriteItem{canonical, recorded, deleteSubmitted}
	if request.Status == taxStatusRejected {
		alert, putErr := a.Accounts.put(record{PK: "TAX_ALERT#REJECTED", SK: now.Format(time.RFC3339Nano) + "#" + request.RequestID, TaxRequest: &request, TaxRequestID: request.RequestID, TaxFolio: request.SIIIssuance.Folio, Status: "OPEN"}, "attribute_not_exists(pk)", nil)
		if putErr != nil {
			return putErr
		}
		items = append(items, alert)
	}
	_, err = a.Accounts.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	return err
}

type directSIISubmissionQuerier struct{ Client sii.Client }

func (d directSIISubmissionQuerier) QuerySubmission(ctx context.Context, credentials sii.Credentials, trackID string) (sii.SubmissionStatus, error) {
	return d.Client.QuerySubmission(ctx, credentials, trackID)
}

func TaxSubmissionStatusHandler(ctx context.Context, _ events.CloudWatchEvent) error {
	worker, err := loadTaxDocumentWorker(ctx)
	if err != nil {
		return err
	}
	app := TaxSubmissionStatusApp{Accounts: worker.Accounts, Config: worker.Config, SII: directSIISubmissionQuerier{Client: sii.Client{HTTP: &http.Client{Timeout: 25 * time.Second}, Now: time.Now}}, Now: time.Now}
	return app.Run(ctx)
}
