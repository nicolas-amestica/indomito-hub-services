package collection

import (
	"context"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
)

type ReceiptResendRequest struct {
	CommandID string `json:"commandId"`
	Email     string `json:"email"`
}

func decodeReceiptResend(req events.APIGatewayV2HTTPRequest) (ReceiptResendRequest, error) {
	var body ReceiptResendRequest
	if len(req.Body) > 1024 || decodeAdmin(req, &body) != nil || !validPortalID(body.CommandID) || len(body.Email) > 254 {
		return body, ErrReplayMismatch
	}
	address, err := mail.ParseAddress(body.Email)
	if err != nil || address.Address != body.Email {
		return body, ErrReplayMismatch
	}
	return body, nil
}

func validReceiptDocument(r record, accountID, receiptID, attemptID string) bool {
	return r.ReceiptID == receiptID && r.Event != nil && r.Event.AccountID == accountID &&
		(attemptID == "" || r.Event.AttemptID == attemptID) &&
		r.DocumentKey == "receipts/"+accountID+"/"+receiptID+"/v"+strconv.FormatInt(receiptDocumentVersion(r), 10)+".pdf" &&
		receiptSHA.MatchString(r.DocumentSHA256)
}

// HandlePortalResend permite una sola copia inmediata, ligada al intento confirmado
// y a la misma sesión que creó el checkout. Nunca acepta un ID de comprobante público.
func (a ReceiptsApp) HandlePortalResend(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if req.RequestContext.HTTP.Method != "POST" || a.Now == nil {
		return portalFailure(405)
	}
	body, err := decodeReceiptResend(req)
	if err != nil {
		return portalFailure(400)
	}
	account, err := a.Accounts.PassengerAccount(ctx, req)
	if err != nil {
		return portalFailure(403)
	}
	attemptID := req.PathParameters["id"]
	sessionID, _ := req.RequestContext.Authorizer.Lambda["paymentSessionId"].(string)
	if !validPortalID(attemptID) || !validPortalID(sessionID) {
		return portalFailure(404)
	}
	attempt, err := a.Accounts.read(ctx, "ATTEMPT#"+attemptID, "META")
	if err != nil || attempt.Attempt == nil || attempt.Attempt.AccountID != account.ID || attempt.PaymentSessionID != sessionID {
		return portalFailure(404)
	}
	outcome, err := a.Accounts.read(ctx, "ATTEMPT#"+attemptID, "OUTCOME")
	if err != nil || outcome.Event == nil || outcome.Event.Type != "PAYMENT_RECEIVED" || outcome.Event.AccountID != account.ID || outcome.Event.AttemptID != attemptID || !strings.HasPrefix(outcome.Event.Reference, "khipu:") {
		return portalFailure(409)
	}
	receiptID := paymentOperationID(outcome.Event.Reference)
	original, err := a.Accounts.read(ctx, "RECEIPT#"+receiptID, "META")
	if err != nil || original.Event == nil || original.Event.Type != "PAYMENT_RECEIVED" || !validReceiptDocument(original, account.ID, receiptID, attemptID) {
		return portalFailure(409)
	}
	return a.enqueueResend(ctx, original, body, "passenger:"+account.ID+":"+sessionID, "PORTAL_RESEND#"+attemptID)
}

// HandleAdminResend admite reenvíos sin límite numérico, siempre auditados e idempotentes.
func (a ReceiptsApp) HandleAdminResend(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if req.RequestContext.HTTP.Method != "POST" || a.Now == nil {
		return portalFailure(405)
	}
	body, err := decodeReceiptResend(req)
	if err != nil {
		return portalFailure(400)
	}
	accountID, err := a.accountID(ctx, req)
	if err != nil {
		return portalFailure(403)
	}
	receiptID := req.PathParameters["id"]
	original, err := a.Accounts.read(ctx, "RECEIPT#"+receiptID, "META")
	if err != nil || original.Event == nil || !validReceiptDocument(original, accountID, receiptID, original.Event.AttemptID) {
		return portalFailure(404)
	}
	userID, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		return portalFailure(403)
	}
	return a.enqueueResend(ctx, original, body, "admin:"+userID, "")
}

func (a ReceiptsApp) enqueueResend(ctx context.Context, original record, body ReceiptResendRequest, actor, oneTimeSK string) events.APIGatewayV2HTTPResponse {
	accountID, receiptID := original.Event.AccountID, original.ReceiptID
	deliveryID := paymentOperationID("receipt-resend:" + accountID + ":" + receiptID + ":" + body.CommandID)
	hash, err := fingerprint(Command{ID: deliveryID, AccountID: accountID, ReceiptID: receiptID, Payload: body})
	if err != nil {
		return portalFailure(503)
	}
	if prior, readErr := a.Accounts.read(ctx, original.PK, "DELIVERY#"+deliveryID); readErr == nil {
		return resendReplay(prior, hash)
	}
	now := a.Now().UTC()
	date := now.Format(time.DateOnly)
	rows := []record{
		{PK: original.PK, SK: "DELIVERY#" + deliveryID, DeliveryID: deliveryID, ReceiptID: receiptID, AccountID: accountID, ReceiptEmail: body.Email, Fingerprint: hash, RequestedBy: actor, Event: original.Event, DocumentKey: original.DocumentKey, DocumentSHA256: original.DocumentSHA256, DocumentVersion: original.DocumentVersion, PassengerName: original.PassengerName, PassengerDocument: original.PassengerDocument, Status: "PENDING_DOCUMENT"},
		{PK: "JOB#" + date, SK: "PENDING#" + deliveryID, DeliveryID: deliveryID, ReceiptID: receiptID, Status: "PENDING"},
	}
	if oneTimeSK != "" {
		rows = append(rows, record{PK: original.PK, SK: oneTimeSK, DeliveryID: deliveryID, RequestedBy: actor, ExpiresAt: now.Add(48 * time.Hour).Unix()})
	}
	writes := make([]types.TransactWriteItem, 0, len(rows))
	for _, row := range rows {
		write, putErr := a.Accounts.put(row, "attribute_not_exists(pk)", nil)
		if putErr != nil {
			return portalFailure(503)
		}
		writes = append(writes, write)
	}
	_, err = a.Accounts.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err != nil {
		if saved, readErr := a.Accounts.read(ctx, original.PK, "DELIVERY#"+deliveryID); readErr == nil {
			return resendReplay(saved, hash)
		}
		if oneTimeSK != "" {
			if _, readErr := a.Accounts.read(ctx, original.PK, oneTimeSK); readErr == nil {
				return portalFailure(409)
			}
		}
		return portalFailure(503)
	}
	return portalResponse(202, map[string]string{"status": "QUEUED", "deliveryId": deliveryID})
}

func resendReplay(row record, hash string) events.APIGatewayV2HTTPResponse {
	if row.Fingerprint != hash {
		return portalFailure(409)
	}
	return portalResponse(202, map[string]string{"status": "QUEUED", "deliveryId": row.DeliveryID})
}
