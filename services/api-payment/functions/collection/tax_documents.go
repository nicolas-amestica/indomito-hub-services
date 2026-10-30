package collection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

const (
	taxStatusPending        = "PENDING_MANUAL_ISSUE"
	taxStatusManualRecorded = "MANUAL_RECORDED"
	taxStatusReady          = "READY"
	taxStatusIssuing        = "ISSUING"
	taxStatusSubmitted      = "SUBMITTED"
	manualSalesReceiptKind  = "BOLETA_VENTA_ELECTRONICA"
	maxManualTaxBodyBytes   = 7 * 1024 * 1024
)

// TaxDocumentRequest separa la obligación tributaria del pago y del comprobante interno.
type TaxDocumentRequest struct {
	SchemaVersion   int                `json:"schemaVersion" dynamodbav:"schemaVersion"`
	RequestID       string             `json:"requestId" dynamodbav:"requestId"`
	SourceEventID   string             `json:"sourceEventId" dynamodbav:"sourceEventId"`
	ReceiptID       string             `json:"receiptId" dynamodbav:"receiptId"`
	AccountID       string             `json:"accountId" dynamodbav:"accountId"`
	TripID          string             `json:"tripId" dynamodbav:"tripId"`
	Currency        string             `json:"currency" dynamodbav:"currency"`
	GrossAmount     int64              `json:"grossAmount" dynamodbav:"grossAmount"`
	EffectiveDate   string             `json:"effectiveDate" dynamodbav:"effectiveDate"`
	RequestedAt     time.Time          `json:"requestedAt" dynamodbav:"requestedAt"`
	DocumentKind    string             `json:"documentKind" dynamodbav:"documentKind"`
	DocumentSubtype string             `json:"documentSubtype" dynamodbav:"documentSubtype"`
	IssuanceMode    string             `json:"issuanceMode" dynamodbav:"issuanceMode"`
	Status          string             `json:"status" dynamodbav:"status"`
	ManualIssuance  *ManualTaxIssuance `json:"manualIssuance,omitempty" dynamodbav:"manualIssuance,omitempty"`
	SIIIssuance     *SIIIssuance       `json:"siiIssuance,omitempty" dynamodbav:"siiIssuance,omitempty"`
	Items           []TaxDocumentItem  `json:"items,omitempty" dynamodbav:"items,omitempty"`
	Reference       string             `json:"reference,omitempty" dynamodbav:"reference,omitempty"`
	CustomerRUT     string             `json:"customerRut,omitempty" dynamodbav:"customerRut,omitempty"`
}

type TaxDocumentItem struct {
	Description string `json:"description" dynamodbav:"description"`
	Quantity    int64  `json:"quantity" dynamodbav:"quantity"`
	UnitPrice   int64  `json:"unitPrice" dynamodbav:"unitPrice"`
	Exempt      bool   `json:"exempt" dynamodbav:"exempt"`
	Unit        string `json:"unit,omitempty" dynamodbav:"unit,omitempty"`
}

// SIIIssuance conserva el folio reservado y el seguimiento del envio oficial.
type SIIIssuance struct {
	Folio       int64      `json:"folio" dynamodbav:"folio"`
	TrackID     string     `json:"trackId,omitempty" dynamodbav:"trackId,omitempty"`
	SubmittedAt *time.Time `json:"submittedAt,omitempty" dynamodbav:"submittedAt,omitempty"`
}

// ManualTaxIssuance registra lo ingresado por un operador, sin afirmar aceptación automática del SII.
type ManualTaxIssuance struct {
	Folio          string       `json:"folio" dynamodbav:"folio"`
	IssueDate      string       `json:"issueDate" dynamodbav:"issueDate"`
	DocumentKey    string       `json:"documentKey" dynamodbav:"documentKey"`
	DocumentSHA256 string       `json:"documentSha256" dynamodbav:"documentSha256"`
	Audit          domain.Audit `json:"audit" dynamodbav:"audit"`
}

type TaxDocumentPage struct {
	Items      []TaxDocumentRequest `json:"items"`
	NextCursor string               `json:"nextCursor,omitempty"`
}

type ManualTaxIssuanceRequest struct {
	CommandID string `json:"commandId"`
	Folio     string `json:"folio"`
	IssueDate string `json:"issueDate"`
	Reason    string `json:"reason"`
	PDFBase64 string `json:"pdfBase64"`
}

type CreateTaxDocumentRequest struct {
	CommandID   string            `json:"commandId"`
	IssueDate   string            `json:"issueDate"`
	CustomerRUT string            `json:"customerRut"`
	Reference   string            `json:"reference"`
	Items       []TaxDocumentItem `json:"items"`
}

type TaxDocumentStorage interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

type TaxDocumentSigner interface {
	Download(context.Context, string) (string, error)
}

type TaxDocumentsApp struct {
	Accounts Service
	Storage  TaxDocumentStorage
	Signer   TaxDocumentSigner
	Bucket   string
	Now      func() time.Time
	IssueNow func(context.Context, string) error
}

func newManualSalesReceiptRequest(receiptID string, event domain.Event) TaxDocumentRequest {
	return TaxDocumentRequest{SchemaVersion: 2, RequestID: receiptID, SourceEventID: event.CommandID, ReceiptID: receiptID, AccountID: event.AccountID, TripID: event.TripID, Currency: "CLP", GrossAmount: event.Amount, EffectiveDate: event.EffectiveDate, RequestedAt: event.RecordedAt.UTC(), DocumentKind: "BOLETA", DocumentSubtype: manualSalesReceiptKind, IssuanceMode: "SII_DIRECT", Status: taxStatusReady}
}

func (a TaxDocumentsApp) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	requestID := req.PathParameters["requestId"]
	if requestID == "" && req.RequestContext.HTTP.Method == "POST" {
		return a.create(ctx, req, actor)
	}
	if requestID == "" && req.RequestContext.HTTP.Method == "GET" {
		return a.list(ctx, req)
	}
	if !validPortalID(requestID) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if req.PathParameters["action"] == "descarga" && req.RequestContext.HTTP.Method == "GET" {
		return a.download(ctx, req, requestID)
	}
	if req.PathParameters["action"] == "emision" && req.RequestContext.HTTP.Method == "POST" {
		if a.IssueNow == nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		if err := a.IssueNow(ctx, requestID); err != nil {
			return adminFailure(req, 409, "PAYMENT_STATE_CONFLICT")
		}
		row, err := a.Accounts.read(ctx, "TAX_REQUEST#"+requestID, "META")
		if err != nil || row.TaxRequest == nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		return lambdautil.SuccessResponseWithHeaders(200, row.TaxRequest, map[string]string{"cache-control": "no-store"})
	}
	if req.RequestContext.HTTP.Method != "POST" {
		return adminFailure(req, 405, "METHOD_NOT_ALLOWED")
	}
	return a.recordManualIssuance(ctx, req, requestID, actor)
}

func (a TaxDocumentsApp) create(ctx context.Context, req events.APIGatewayV2HTTPRequest, actor string) (events.APIGatewayV2HTTPResponse, error) {
	var body CreateTaxDocumentRequest
	if a.Now == nil || decodeAdmin(req, &body) != nil || !validPortalID(body.CommandID) || len(body.Items) == 0 || len(body.Items) > 20 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	issueDate, err := time.Parse(time.DateOnly, body.IssueDate)
	if err != nil || issueDate.After(a.Now().UTC().Add(24*time.Hour)) || len(strings.TrimSpace(body.Reference)) > 90 || len(strings.TrimSpace(body.CustomerRUT)) > 12 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	var total int64
	for index := range body.Items {
		item := &body.Items[index]
		item.Description = strings.TrimSpace(item.Description)
		item.Unit = strings.TrimSpace(item.Unit)
		if item.Description == "" || len(item.Description) > 80 || item.Quantity <= 0 || item.Quantity > 100000 || item.UnitPrice <= 0 || item.UnitPrice > 100000000 || len(item.Unit) > 4 {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		line := item.Quantity * item.UnitPrice
		if line <= 0 || total > 999999999-line {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		total += line
	}
	requestID := body.CommandID
	if prior, readErr := a.Accounts.read(ctx, "COMMAND#"+requestID, "META"); readErr == nil && prior.TaxRequest != nil {
		return lambdautil.SuccessResponseWithHeaders(200, prior.TaxRequest, map[string]string{"cache-control": "no-store"})
	}
	now := a.Now().UTC()
	tax := TaxDocumentRequest{SchemaVersion: 3, RequestID: requestID, SourceEventID: requestID, Currency: "CLP", GrossAmount: total, EffectiveDate: issueDate.Format(time.DateOnly), RequestedAt: now, DocumentKind: "BOLETA", DocumentSubtype: manualSalesReceiptKind, IssuanceMode: "SII_DIRECT", Status: taxStatusReady, Items: body.Items, Reference: strings.TrimSpace(body.Reference), CustomerRUT: strings.TrimSpace(body.CustomerRUT)}
	canonical, err := a.Accounts.put(record{PK: "TAX_REQUEST#" + requestID, SK: "META", TaxRequest: &tax, Status: tax.Status}, "attribute_not_exists(pk)", nil)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	queue, err := a.Accounts.put(record{PK: "TAX_QUEUE#PENDING", SK: now.Format(time.RFC3339Nano) + "#" + requestID, TaxRequest: &tax, Status: tax.Status}, "attribute_not_exists(pk)", nil)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	command, err := a.Accounts.put(record{PK: "COMMAND#" + requestID, SK: "META", TaxRequest: &tax, Status: tax.Status, RequestedBy: actor}, "attribute_not_exists(pk)", nil)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	if _, err = a.Accounts.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{canonical, queue, command}}); err != nil {
		if saved, readErr := a.Accounts.read(ctx, "COMMAND#"+requestID, "META"); readErr == nil && saved.TaxRequest != nil {
			return lambdautil.SuccessResponseWithHeaders(200, saved.TaxRequest, map[string]string{"cache-control": "no-store"})
		}
		return adminFailure(req, 409, "PAYMENT_STATE_CONFLICT")
	}
	return lambdautil.SuccessResponseWithHeaders(202, tax, map[string]string{"cache-control": "no-store"})
}

func (a TaxDocumentsApp) list(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	db, ok := a.Accounts.DB.(queryDatabase)
	if !ok {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	status, queuePK := strings.ToUpper(req.QueryStringParameters["status"]), "TAX_QUEUE#PENDING"
	if status == "RECORDED" {
		queuePK = "TAX_QUEUE#RECORDED"
	} else if status != "" && status != "PENDING" {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	cursor := req.QueryStringParameters["cursor"]
	if cursor != "" && (!strings.Contains(cursor, "#") || len(cursor) > 100) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	input := &dynamodb.QueryInput{TableName: &a.Accounts.Table, KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: queuePK}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(20), ScanIndexForward: aws.Bool(false)}
	if cursor != "" {
		input.ExclusiveStartKey = key(queuePK, cursor)
	}
	out, err := db.Query(ctx, input)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	page := TaxDocumentPage{Items: []TaxDocumentRequest{}}
	for _, item := range out.Items {
		var row record
		if attributevalue.UnmarshalMap(item, &row) != nil || row.TaxRequest == nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		page.Items = append(page.Items, *row.TaxRequest)
	}
	if sk, ok := out.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
		page.NextCursor = sk.Value
	}
	return lambdautil.SuccessResponseWithHeaders(200, page, map[string]string{"cache-control": "no-store"})
}

var taxFolioPattern = regexp.MustCompile(`^[0-9]{1,18}$`)

func (a TaxDocumentsApp) recordManualIssuance(ctx context.Context, req events.APIGatewayV2HTTPRequest, requestID, actor string) (events.APIGatewayV2HTTPResponse, error) {
	var body ManualTaxIssuanceRequest
	if a.Now == nil || a.Storage == nil || a.Bucket == "" || decodeManualTaxIssuance(req, &body) != nil || !validPortalID(body.CommandID) || !taxFolioPattern.MatchString(strings.TrimSpace(body.Folio)) || len(strings.TrimSpace(body.Reason)) < 5 || len(body.Reason) > 500 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	issueDate, err := time.Parse(time.DateOnly, body.IssueDate)
	if err != nil || issueDate.After(a.Now().UTC().Add(24*time.Hour)) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	commandID := paymentOperationID("manual-tax-issuance:" + requestID + ":" + body.CommandID)
	if prior, readErr := a.Accounts.read(ctx, "COMMAND#"+commandID, "META"); readErr == nil && prior.TaxRequest != nil {
		return lambdautil.SuccessResponseWithHeaders(200, prior.TaxRequest, map[string]string{"cache-control": "no-store"})
	}
	current, err := a.Accounts.read(ctx, "TAX_REQUEST#"+requestID, "META")
	if err != nil || current.TaxRequest == nil || current.TaxRequest.RequestID != requestID {
		return domainFailure(req, err)
	}
	if (current.TaxRequest.Status != taxStatusPending && current.TaxRequest.Status != taxStatusReady) || current.TaxRequest.ManualIssuance != nil || current.TaxRequest.SIIIssuance != nil {
		return adminFailure(req, 409, "PAYMENT_STATE_CONFLICT")
	}
	if issueDate.Format(time.DateOnly) < current.TaxRequest.EffectiveDate {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	pdf, err := base64.StdEncoding.DecodeString(body.PDFBase64)
	if err != nil || len(pdf) < 5 || len(pdf) > 5*1024*1024 || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	sum := sha256.Sum256(pdf)
	digest := hex.EncodeToString(sum[:])
	documentKey := "tax-documents/manual/" + requestID + "/" + digest + ".pdf"
	if err = a.putTaxPDF(ctx, documentKey, digest, pdf); err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	audit := domain.Audit{CommandID: commandID, Actor: actor, Reason: strings.TrimSpace(body.Reason), RecordedAt: a.Now().UTC()}
	issued := *current.TaxRequest
	issued.Status = taxStatusManualRecorded
	issued.ManualIssuance = &ManualTaxIssuance{Folio: strings.TrimSpace(body.Folio), IssueDate: issueDate.Format(time.DateOnly), DocumentKey: documentKey, DocumentSHA256: digest, Audit: audit}
	canonical, err := a.Accounts.put(record{PK: "TAX_REQUEST#" + requestID, SK: "META", TaxRequest: &issued, Status: issued.Status, AccountID: issued.AccountID, TripID: issued.TripID}, "#status = :pending", map[string]types.AttributeValue{":pending": &types.AttributeValueMemberS{Value: current.TaxRequest.Status}})
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	queueSK := current.TaxRequest.RequestedAt.UTC().Format(time.RFC3339Nano) + "#" + requestID
	deleteQueue := types.TransactWriteItem{Delete: &types.Delete{TableName: &a.Accounts.Table, Key: key("TAX_QUEUE#PENDING", queueSK), ConditionExpression: aws.String("#status = :pending"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":pending": &types.AttributeValueMemberS{Value: current.TaxRequest.Status}}}}
	command, err := a.Accounts.put(record{PK: "COMMAND#" + commandID, SK: "META", TaxRequest: &issued, Status: issued.Status}, "attribute_not_exists(pk)", nil)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	issuance, err := a.Accounts.put(record{PK: "TAX_REQUEST#" + requestID, SK: "ISSUANCE#MANUAL", TaxRequest: &issued, Status: issued.Status}, "attribute_not_exists(pk)", nil)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	recordedProjection, err := a.Accounts.put(record{PK: "TAX_QUEUE#RECORDED", SK: audit.RecordedAt.Format(time.RFC3339Nano) + "#" + requestID, TaxRequest: &issued, Status: issued.Status, AccountID: issued.AccountID, TripID: issued.TripID}, "attribute_not_exists(pk)", nil)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	folioReservation, err := a.Accounts.put(record{PK: "TAX_FOLIO#" + manualSalesReceiptKind + "#" + issued.ManualIssuance.Folio, SK: "META", TaxRequest: &issued, Status: issued.Status}, "attribute_not_exists(pk)", nil)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	_, err = a.Accounts.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{canonical, deleteQueue, issuance, recordedProjection, folioReservation, command}})
	if err != nil {
		if saved, readErr := a.Accounts.read(ctx, "COMMAND#"+commandID, "META"); readErr == nil && saved.TaxRequest != nil {
			return lambdautil.SuccessResponseWithHeaders(200, saved.TaxRequest, map[string]string{"cache-control": "no-store"})
		}
		return adminFailure(req, 409, "PAYMENT_STATE_CONFLICT")
	}
	return lambdautil.SuccessResponseWithHeaders(200, issued, map[string]string{"cache-control": "no-store"})
}

// decodeManualTaxIssuance aplica un limite exclusivo para el PDF codificado.
// El decoder administrativo general conserva su limite pequeno de 64 KiB.
func decodeManualTaxIssuance(req events.APIGatewayV2HTTPRequest, target *ManualTaxIssuanceRequest) error {
	body := req.Body
	if req.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return domain.ErrInvalid
		}
		if len(decoded) > maxManualTaxBodyBytes {
			return domain.ErrInvalid
		}
		body = string(decoded)
	}
	if len(body) > maxManualTaxBodyBytes {
		return domain.ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return domain.ErrInvalid
	}
	return nil
}

func (a TaxDocumentsApp) putTaxPDF(ctx context.Context, key, digest string, pdf []byte) error {
	_, err := a.Storage.PutObject(ctx, &s3.PutObjectInput{Bucket: &a.Bucket, Key: &key, Body: bytes.NewReader(pdf), ContentType: aws.String("application/pdf"), ContentDisposition: aws.String(`attachment; filename="boleta-sii.pdf"`), ServerSideEncryption: "AES256", IfNoneMatch: aws.String("*"), Metadata: map[string]string{"sha256": digest}})
	if err == nil {
		return nil
	}
	head, headErr := a.Storage.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &a.Bucket, Key: &key})
	if headErr == nil && head.Metadata["sha256"] == digest {
		return nil
	}
	return err
}

func (a TaxDocumentsApp) download(ctx context.Context, req events.APIGatewayV2HTTPRequest, requestID string) (events.APIGatewayV2HTTPResponse, error) {
	row, err := a.Accounts.read(ctx, "TAX_REQUEST#"+requestID, "META")
	if err != nil || row.TaxRequest == nil || row.TaxRequest.ManualIssuance == nil || a.Signer == nil {
		return adminFailure(req, 404, "PAYMENT_ACCOUNT_NOT_FOUND")
	}
	issuance := row.TaxRequest.ManualIssuance
	expectedPrefix := "tax-documents/manual/" + requestID + "/"
	if !strings.HasPrefix(issuance.DocumentKey, expectedPrefix) || !receiptSHA.MatchString(issuance.DocumentSHA256) {
		return adminFailure(req, 409, "PAYMENT_STATE_CONFLICT")
	}
	url, err := a.Signer.Download(ctx, issuance.DocumentKey)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return lambdautil.SuccessResponseWithHeaders(200, map[string]any{"url": url, "expiresIn": 120}, map[string]string{"cache-control": "no-store"})
}
