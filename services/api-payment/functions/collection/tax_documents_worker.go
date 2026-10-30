package collection

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
)

type SIIConfigLoader interface {
	Load(context.Context, time.Time) (sii.Credentials, error)
}

type SIIDocumentIssuer interface {
	Issue(context.Context, sii.Credentials, TaxDocumentRequest, int64, time.Time) (sii.Submission, error)
}

type TaxDocumentWorkerApp struct {
	Accounts Service
	Config   SIIConfigLoader
	Issuer   SIIDocumentIssuer
	Now      func() time.Time
}

func (a TaxDocumentWorkerApp) Handle(ctx context.Context, event events.DynamoDBEvent) events.DynamoDBEventResponse {
	response := events.DynamoDBEventResponse{BatchItemFailures: []events.DynamoDBBatchItemFailure{}}
	for _, change := range event.Records {
		pk, pkOK := streamString(change.Change.NewImage, "pk")
		status, statusOK := streamString(change.Change.NewImage, "status")
		if !pkOK || !statusOK || pk != "TAX_QUEUE#PENDING" || status != taxStatusReady || a.Config == nil || a.Issuer == nil || a.Now == nil {
			continue
		}
		requestID, idOK := streamTaxRequestID(change.Change.NewImage)
		if !idOK || a.Process(ctx, requestID) != nil {
			response.BatchItemFailures = append(response.BatchItemFailures, events.DynamoDBBatchItemFailure{ItemIdentifier: change.EventID})
		}
	}
	return response
}

func (a TaxDocumentWorkerApp) Process(ctx context.Context, requestID string) error {
	now := a.Now().UTC()
	credentials, err := a.Config.Load(ctx, now)
	if err != nil {
		return fmt.Errorf("load SII config: %w", err)
	}
	request, err := a.reserveFolio(ctx, requestID, credentials)
	if err != nil {
		return err
	}
	if request.Status == taxStatusSubmitted {
		return nil
	}
	if request.SIIIssuance == nil || request.SIIIssuance.Folio < credentials.FolioFrom || request.SIIIssuance.Folio > credentials.FolioTo {
		return errors.New("invalid reserved tax folio")
	}
	submission, err := a.Issuer.Issue(ctx, credentials, request, request.SIIIssuance.Folio, now)
	if err != nil {
		return fmt.Errorf("submit SII document: %w", err)
	}
	return a.recordSubmission(ctx, request, submission, now)
}

func (a TaxDocumentWorkerApp) reserveFolio(ctx context.Context, requestID string, credentials sii.Credentials) (TaxDocumentRequest, error) {
	current, err := a.Accounts.read(ctx, "TAX_REQUEST#"+requestID, "META")
	if err != nil || current.TaxRequest == nil {
		return TaxDocumentRequest{}, err
	}
	request := *current.TaxRequest
	if request.Status == taxStatusSubmitted || (request.Status == taxStatusIssuing && request.SIIIssuance != nil) {
		return request, nil
	}
	if request.Status != taxStatusReady || request.GrossAmount <= 0 || request.Currency != "CLP" || request.DocumentSubtype != manualSalesReceiptKind {
		return TaxDocumentRequest{}, errors.New("invalid tax request state")
	}
	folioState, stateErr := a.Accounts.read(ctx, "TAX_FOLIO#39", "META")
	nextFolio := credentials.FolioFrom
	condition := "attribute_not_exists(pk)"
	values := map[string]types.AttributeValue{}
	if stateErr == nil {
		nextFolio = folioState.Count + 1
		condition = "#count = :previous"
		values[":previous"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(folioState.Count, 10)}
	} else if !errors.Is(stateErr, ErrNotFound) {
		return TaxDocumentRequest{}, stateErr
	}
	if nextFolio < credentials.FolioFrom || nextFolio > credentials.FolioTo {
		return TaxDocumentRequest{}, errors.New("SII CAF folios exhausted")
	}
	request.Status = taxStatusIssuing
	request.SIIIssuance = &SIIIssuance{Folio: nextFolio}
	canonical, err := a.Accounts.put(record{PK: "TAX_REQUEST#" + requestID, SK: "META", TaxRequest: &request, Status: request.Status, AccountID: request.AccountID, TripID: request.TripID}, "#status = :pending", map[string]types.AttributeValue{":pending": &types.AttributeValueMemberS{Value: taxStatusReady}})
	if err != nil {
		return TaxDocumentRequest{}, err
	}
	folio, err := a.Accounts.put(record{PK: "TAX_FOLIO#39", SK: "META", Count: nextFolio, TaxRequest: &request, Status: "RESERVED"}, condition, values)
	if err != nil {
		return TaxDocumentRequest{}, err
	}
	_, err = a.Accounts.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{canonical, folio}})
	if err != nil {
		if saved, readErr := a.Accounts.read(ctx, "TAX_REQUEST#"+requestID, "META"); readErr == nil && saved.TaxRequest != nil && saved.TaxRequest.Status == taxStatusSubmitted {
			return *saved.TaxRequest, nil
		}
		return TaxDocumentRequest{}, fmt.Errorf("tax issuance already in progress: %w", err)
	}
	return request, nil
}

func (a TaxDocumentWorkerApp) recordSubmission(ctx context.Context, request TaxDocumentRequest, submission sii.Submission, now time.Time) error {
	if submission.TrackID == "" || request.SIIIssuance == nil {
		return errors.New("invalid SII submission")
	}
	request.Status = taxStatusSubmitted
	request.SIIIssuance.TrackID = submission.TrackID
	request.SIIIssuance.SubmittedAt = &now
	canonical, err := a.Accounts.put(record{PK: "TAX_REQUEST#" + request.RequestID, SK: "META", TaxRequest: &request, Status: request.Status, AccountID: request.AccountID, TripID: request.TripID}, "#status = :pending", map[string]types.AttributeValue{":pending": &types.AttributeValueMemberS{Value: taxStatusIssuing}})
	if err != nil {
		return err
	}
	queueSK := request.RequestedAt.UTC().Format(time.RFC3339Nano) + "#" + request.RequestID
	deleteQueue := types.TransactWriteItem{Delete: &types.Delete{TableName: &a.Accounts.Table, Key: key("TAX_QUEUE#PENDING", queueSK), ConditionExpression: aws.String("#status = :pending"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":pending": &types.AttributeValueMemberS{Value: taxStatusReady}}}}
	recorded, err := a.Accounts.put(record{PK: "TAX_QUEUE#RECORDED", SK: now.Format(time.RFC3339Nano) + "#" + request.RequestID, TaxRequest: &request, Status: request.Status, AccountID: request.AccountID, TripID: request.TripID}, "attribute_not_exists(pk)", nil)
	if err != nil {
		return err
	}
	_, err = a.Accounts.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{canonical, deleteQueue, recorded}})
	return err
}

func streamTaxRequestID(image map[string]events.DynamoDBAttributeValue) (string, bool) {
	value, ok := image["taxRequest"]
	if !ok || value.DataType() != events.DataTypeMap {
		return "", false
	}
	id, ok := value.Map()["requestId"]
	return id.String(), ok && id.DataType() == events.DataTypeString && validPortalID(id.String())
}

type directSIIssuer struct{ Client sii.Client }

func (d directSIIssuer) Issue(ctx context.Context, credentials sii.Credentials, request TaxDocumentRequest, folio int64, now time.Time) (sii.Submission, error) {
	issuedAt, err := time.Parse(time.DateOnly, request.EffectiveDate)
	if err != nil {
		return sii.Submission{}, err
	}
	items := make([]sii.SaleItem, 0, len(request.Items))
	for _, item := range request.Items {
		items = append(items, sii.SaleItem{Description: item.Description, Quantity: item.Quantity, UnitPrice: item.UnitPrice, Exempt: item.Exempt, Unit: item.Unit})
	}
	description := "Cuota de gira de estudios"
	if len(items) > 0 {
		description = items[0].Description
	}
	document, err := sii.BuildDocument(credentials, sii.Sale{RequestID: request.RequestID, Folio: folio, Amount: request.GrossAmount, IssueDate: issuedAt, CustomerRUT: request.CustomerRUT, Description: description, Items: items, Reference: request.Reference}, now)
	if err != nil {
		return sii.Submission{}, err
	}
	return d.Client.Submit(ctx, credentials, document)
}

var _ SIIDocumentIssuer = directSIIssuer{}
