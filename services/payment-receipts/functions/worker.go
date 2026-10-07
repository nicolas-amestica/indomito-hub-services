package functions

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

type receiptStore interface {
	get(context.Context, string) (receiptRow, error)
	claim(context.Context, string, string) (bool, error)
	documentReady(context.Context, string, string, string, string) error
	sent(context.Context, string, string) error
	failed(context.Context, string, string, string) error
	finishJob(context.Context, string, string, string, string) error
}

type documentStore interface {
	putImmutable(context.Context, renderedDocument) error
}
type receiptMailer interface {
	send(context.Context, string, string, string, []byte, bool) error
}

type dependencies struct {
	store         receiptStore
	deliveryStore func(string) receiptStore
	documents     documentStore
	mail          receiptMailer
}

func processReceipt(ctx context.Context, receiptID string, deps dependencies) (string, error) {
	if !receiptIDPattern.MatchString(receiptID) {
		return "", errors.New("INVALID_RECEIPT_ID")
	}
	row, err := deps.store.get(ctx, receiptID)
	if err != nil {
		return "", err
	}
	if row.Status == statusSent || row.Status == statusDeliveryFailed {
		return row.Status, nil
	}
	model, err := buildReceiptModel(row)
	if err != nil {
		return "", err
	}
	owner, err := randomOwner()
	if err != nil {
		return "", err
	}
	claimed, err := deps.store.claim(ctx, receiptID, owner)
	if err != nil {
		return "", err
	}
	if !claimed {
		return "", errors.New("RECEIPT_BUSY_OR_RETRY_LIMIT")
	}
	fail := func(cause error) (string, error) {
		code := cause.Error()
		if !allowedFailure(code) {
			code = "DELIVERY_DEPENDENCY_FAILURE"
		}
		if failureErr := deps.store.failed(ctx, receiptID, owner, code); failureErr != nil {
			return "", fmt.Errorf("%v; registrar fallo: %w", cause, failureErr)
		}
		return "", cause
	}
	document, err := renderReceipt(model)
	if err != nil {
		return fail(err)
	}
	if row.DocumentSHA256 != "" && (row.DocumentSHA256 != document.SHA256 || row.DocumentKey != document.Key) {
		return fail(errors.New("IMMUTABLE_RECEIPT_CONFLICT"))
	}
	if err = deps.documents.putImmutable(ctx, document); err != nil {
		return fail(err)
	}
	if err = deps.store.documentReady(ctx, receiptID, owner, document.Key, document.SHA256); err != nil {
		return fail(err)
	}
	if row.DeliveryMode == "DOCUMENT_ONLY" {
		return statusDocumentReady, nil
	}
	address, mailErr := mail.ParseAddress(row.ReceiptEmail)
	if mailErr != nil || address.Address != row.ReceiptEmail || len(row.ReceiptEmail) > 254 {
		return fail(errors.New("RECEIPT_EMAIL_MISSING"))
	}
	deliveryID := row.DeliveryID
	if deliveryID == "" {
		deliveryID = receiptID
	}
	if err = deps.mail.send(ctx, row.ReceiptEmail, model.ID, deliveryID, document.Bytes, model.Review); err != nil {
		return fail(err)
	}
	if err = deps.store.sent(ctx, receiptID, owner); err != nil {
		return "", err
	}
	return statusSent, nil
}

func processJob(ctx context.Context, value job, deps dependencies) (string, error) {
	id := value.ReceiptID
	selected := deps
	if value.DeliveryID != "" {
		id = value.DeliveryID
		selected.store = deps.deliveryStore(value.ReceiptID)
	}
	if !receiptIDPattern.MatchString(id) || !receiptIDPattern.MatchString(value.ReceiptID) || value.SK != "PENDING#"+id || !strings.HasPrefix(value.PK, "JOB#") || len(value.PK) != len("JOB#2006-01-02") {
		return "", errors.New("JOB_RECEIPT_MISMATCH")
	}
	status, err := processReceipt(ctx, id, selected)
	if err != nil {
		return "", err
	}
	if err = deps.store.finishJob(ctx, strings.TrimPrefix(value.PK, "JOB#"), id, status, value.ReceiptID); err != nil {
		return "", err
	}
	return status, nil
}

func processStream(ctx context.Context, event events.DynamoDBEvent, deps dependencies) events.DynamoDBEventResponse {
	response := events.DynamoDBEventResponse{BatchItemFailures: []events.DynamoDBBatchItemFailure{}}
	for _, change := range event.Records {
		pk, pkOK := streamString(change.Change.NewImage, "pk")
		sk, skOK := streamString(change.Change.NewImage, "sk")
		status, statusOK := streamString(change.Change.NewImage, "status")
		if change.EventName != "INSERT" && change.EventName != "MODIFY" || !pkOK || !skOK || !statusOK || !strings.HasPrefix(pk, "JOB#") || !strings.HasPrefix(sk, "PENDING#") || status != "PENDING" {
			continue
		}
		receiptID, _ := streamString(change.Change.NewImage, "receiptId")
		deliveryID, _ := streamString(change.Change.NewImage, "deliveryId")
		result, err := processJob(ctx, job{PK: pk, SK: sk, ReceiptID: receiptID, DeliveryID: deliveryID}, deps)
		if err != nil || result == statusDeliveryFailed {
			identifier := change.Change.SequenceNumber
			if identifier == "" {
				identifier = change.EventID
			}
			response.BatchItemFailures = append(response.BatchItemFailures, events.DynamoDBBatchItemFailure{ItemIdentifier: identifier})
		}
	}
	return response
}

func streamString(image map[string]events.DynamoDBAttributeValue, name string) (string, bool) {
	value, ok := image[name]
	if !ok || value.DataType() != events.DataTypeString || value.String() == "" {
		return "", false
	}
	return value.String(), true
}
