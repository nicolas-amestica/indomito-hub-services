package functions

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-contract/domain"
)

const maxSignedPDFSize int64 = 25 * 1024 * 1024
const signedUploadValidity = 15 * time.Minute

var signedUploadIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,80}$`)
var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type prepareSignedDocumentRequest struct {
	ClientRequestID       string `json:"clientRequestId"`
	Size                  int64  `json:"size"`
	SHA256                string `json:"sha256"`
	CurrentDocumentID     string `json:"currentDocumentId"`
	ReplacementReason     string `json:"replacementReason"`
	ConfirmsAllSignatures bool   `json:"confirmsAllSignatures"`
}

type finalizeSignedDocumentRequest struct {
	ClientRequestID string `json:"clientRequestId"`
}

func HandlePrepareSignedDocument(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	actor, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		return errorResponse(http.StatusUnauthorized, "No se pudo identificar al usuario."), nil
	}
	var body prepareSignedDocumentRequest
	if err = lambdautil.BindJSON(req, &body); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	body.ClientRequestID = strings.TrimSpace(body.ClientRequestID)
	body.SHA256 = strings.ToLower(strings.TrimSpace(body.SHA256))
	body.CurrentDocumentID = strings.TrimSpace(body.CurrentDocumentID)
	body.ReplacementReason = strings.TrimSpace(body.ReplacementReason)
	if !signedUploadIDPattern.MatchString(body.ClientRequestID) || !sha256Pattern.MatchString(body.SHA256) || body.Size < 5 || body.Size > maxSignedPDFSize {
		return errorResponse(400, "La identificación, huella o tamaño del PDF firmado no es válido."), nil
	}
	if len([]rune(body.ReplacementReason)) > 500 {
		return errorResponse(400, "El motivo de reemplazo no puede superar 500 caracteres."), nil
	}
	if !body.ConfirmsAllSignatures {
		return errorResponse(400, "Debe confirmar que el documento corresponde al contrato y contiene todas las firmas."), nil
	}
	contract, code, err := getItem(ctx, app, req.PathParameters[ContractIDParam])
	if err != nil {
		return errorResponse(code, err.Error()), nil
	}
	if contract.Status != domain.StatusApproved || contract.PDFDocument == nil {
		return errorResponse(409, "Solo se puede cargar la copia firmada de un contrato aprobado."), nil
	}
	activeID := ""
	if contract.SignedDocument != nil {
		activeID = contract.SignedDocument.ID
	}
	if body.CurrentDocumentID != activeID {
		return errorResponse(409, "La versión firmada cambió. Recargue el contrato antes de continuar."), nil
	}
	if activeID != "" && body.ReplacementReason == "" {
		return errorResponse(400, "El motivo de reemplazo es obligatorio."), nil
	}

	intent := domain.SignedUploadIntent{
		PK: domain.PK(contract.ID), SK: domain.SignedUploadSK(body.ClientRequestID), Entity: "CONTRACT_SIGNED_UPLOAD",
		ContractID: contract.ID, ID: body.ClientRequestID, ObjectKey: fmt.Sprintf("contracts/%s/signed/%s.pdf", contract.ID, body.ClientRequestID),
		ExpectedSize: body.Size, ExpectedSHA256: body.SHA256, ApprovedVersion: contract.Version,
		ApprovedPDFSHA256: contract.PDFDocument.SHA256, ExpectedActiveDocumentID: activeID,
		ReplacementReason: body.ReplacementReason, Status: "PENDING", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), CreatedBy: actor,
	}
	stored, found, readErr := getSignedUploadIntent(ctx, app, contract.ID, body.ClientRequestID)
	if readErr != nil {
		return errorResponse(500, "No se pudo recuperar la carga firmada."), nil
	}
	if found {
		if stored.ExpectedSize != intent.ExpectedSize || stored.ExpectedSHA256 != intent.ExpectedSHA256 || stored.ExpectedActiveDocumentID != intent.ExpectedActiveDocumentID || stored.ReplacementReason != intent.ReplacementReason {
			return errorResponse(409, "La clave de idempotencia ya fue usada con otro archivo."), nil
		}
		intent = stored
	} else {
		raw, marshalErr := attributevalue.MarshalMap(intent)
		if marshalErr != nil {
			return errorResponse(500, "No se pudo preparar la carga firmada."), nil
		}
		_, putErr := app.DDB.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(app.Config.ProgramsTableName), Item: raw, ConditionExpression: aws.String("attribute_not_exists(pk)")})
		if putErr != nil {
			stored, found, readErr = getSignedUploadIntent(ctx, app, contract.ID, body.ClientRequestID)
			if readErr != nil || !found || stored.ExpectedSHA256 != intent.ExpectedSHA256 || stored.ExpectedSize != intent.ExpectedSize {
				return errorResponse(409, "La carga ya fue preparada por otra operación."), nil
			}
			intent = stored
		}
	}
	if intent.Status == "COMPLETED" {
		return lambdautil.SuccessResponse(200, map[string]any{"clientRequestId": intent.ID, "status": intent.Status})
	}
	store, ok := app.Documents.(SignedDocumentStore)
	if !ok {
		return errorResponse(500, "El almacenamiento no admite cargas firmadas."), nil
	}
	uploadURL, err := store.PresignSignedPDFUpload(ctx, intent.ObjectKey, signedUploadValidity)
	if err != nil {
		return errorResponse(500, "No se pudo preparar la carga del PDF firmado."), nil
	}
	return lambdautil.SuccessResponse(200, map[string]any{
		"clientRequestId": intent.ID, "uploadUrl": uploadURL, "method": "PUT", "contentType": approvedPDFContentType,
		"expiresAt": time.Now().UTC().Add(signedUploadValidity).Format(time.RFC3339), "maxSize": maxSignedPDFSize,
	})
}

func HandleFinalizeSignedDocument(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	actor, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		return errorResponse(http.StatusUnauthorized, "No se pudo identificar al usuario."), nil
	}
	var body finalizeSignedDocumentRequest
	if err = lambdautil.BindJSON(req, &body); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	body.ClientRequestID = strings.TrimSpace(body.ClientRequestID)
	if !signedUploadIDPattern.MatchString(body.ClientRequestID) {
		return errorResponse(400, "La identificación de carga no es válida."), nil
	}
	contractID := req.PathParameters[ContractIDParam]
	intent, found, err := getSignedUploadIntent(ctx, app, contractID, body.ClientRequestID)
	if err != nil {
		return errorResponse(500, "No se pudo recuperar la carga firmada."), nil
	}
	if !found {
		return errorResponse(404, "La carga firmada no existe."), nil
	}
	contract, code, err := getItem(ctx, app, contractID)
	if err != nil {
		return errorResponse(code, err.Error()), nil
	}
	if contract.SignedDocument != nil && contract.SignedDocument.ID == intent.ID && intent.Status == "COMPLETED" {
		return lambdautil.SuccessResponse(200, contract.SignedDocument)
	}
	if contract.Status != domain.StatusApproved || contract.PDFDocument == nil || contract.Version != intent.ApprovedVersion || contract.PDFDocument.SHA256 != intent.ApprovedPDFSHA256 {
		return errorResponse(409, "El contrato aprobado no coincide con la carga preparada."), nil
	}
	store, ok := app.Documents.(SignedDocumentStore)
	if !ok {
		return errorResponse(500, "El almacenamiento no admite cargas firmadas."), nil
	}
	if err = store.VerifySignedPDF(ctx, intent.ObjectKey, intent.ExpectedSHA256, intent.ExpectedSize); err != nil {
		return errorResponse(422, "El archivo cargado no es un PDF válido o no coincide con la huella preparada."), nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	document := domain.SignedDocument{ID: intent.ID, ObjectKey: intent.ObjectKey, ContentType: approvedPDFContentType, Size: intent.ExpectedSize, SHA256: intent.ExpectedSHA256, ApprovedVersion: intent.ApprovedVersion, ApprovedPDFSHA256: intent.ApprovedPDFSHA256, UploadedAt: now, UploadedBy: actor, ReplacementReason: intent.ReplacementReason}
	documentItem := domain.SignedDocumentItem{PK: domain.PK(contractID), SK: domain.SignedDocumentSK(intent.ID), Entity: "CONTRACT_SIGNED_DOCUMENT", ContractID: contractID, SignedDocument: document}
	documentRaw, err := attributevalue.MarshalMap(documentItem)
	if err != nil {
		return errorResponse(500, "No se pudo publicar el PDF firmado."), nil
	}
	documentValue, err := attributevalue.Marshal(document)
	if err != nil {
		return errorResponse(500, "No se pudo publicar el PDF firmado."), nil
	}
	condition := "#status = :approved AND version = :version AND attribute_not_exists(signedDocument)"
	contractValues := map[string]ddbtypes.AttributeValue{
		":approved": &ddbtypes.AttributeValueMemberS{Value: string(domain.StatusApproved)}, ":version": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprint(intent.ApprovedVersion)},
		":signatureStatus": &ddbtypes.AttributeValueMemberS{Value: string(domain.SignatureUploaded)}, ":document": documentValue,
		":updatedAt": &ddbtypes.AttributeValueMemberS{Value: now},
	}
	if intent.ExpectedActiveDocumentID != "" {
		condition = "#status = :approved AND version = :version AND signedDocument.id = :activeId"
		contractValues[":activeId"] = &ddbtypes.AttributeValueMemberS{Value: intent.ExpectedActiveDocumentID}
	}
	writes := []ddbtypes.TransactWriteItem{
		{Update: &ddbtypes.Update{TableName: aws.String(app.Config.ProgramsTableName), Key: contractKey(contractID), ConditionExpression: aws.String(condition), UpdateExpression: aws.String("SET signatureStatus = :signatureStatus, signedDocument = :document, updatedAt = :updatedAt"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: contractValues}},
		{Put: &ddbtypes.Put{TableName: aws.String(app.Config.ProgramsTableName), Item: documentRaw, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Update: &ddbtypes.Update{TableName: aws.String(app.Config.ProgramsTableName), Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: intent.PK}, "sk": &ddbtypes.AttributeValueMemberS{Value: intent.SK}}, ConditionExpression: aws.String("#status = :pending"), UpdateExpression: aws.String("SET #status = :completed, completedAt = :updatedAt"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":pending": &ddbtypes.AttributeValueMemberS{Value: "PENDING"}, ":completed": &ddbtypes.AttributeValueMemberS{Value: "COMPLETED"}, ":updatedAt": &ddbtypes.AttributeValueMemberS{Value: now}}}},
	}
	_, err = app.DDBTransactions.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err != nil {
		persisted, _, readErr := getItem(ctx, app, contractID)
		if readErr == nil && persisted.SignedDocument != nil && persisted.SignedDocument.ID == intent.ID {
			return lambdautil.SuccessResponse(200, persisted.SignedDocument)
		}
		return errorResponse(409, "Otra versión firmada fue publicada. Recargue el contrato antes de reemplazarla."), nil
	}
	return lambdautil.SuccessResponse(200, document)
}

func HandleListSignedDocuments(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	contractID := req.PathParameters[ContractIDParam]
	if _, code, getErr := getItem(ctx, app, contractID); getErr != nil {
		return errorResponse(code, getErr.Error()), nil
	}
	input := &dynamodb.QueryInput{TableName: aws.String(app.Config.ProgramsTableName), KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":pk": &ddbtypes.AttributeValueMemberS{Value: domain.PK(contractID)}, ":prefix": &ddbtypes.AttributeValueMemberS{Value: domain.SignedDocumentSKPrefix}}, ScanIndexForward: aws.Bool(false), Limit: aws.Int32(20)}
	if cursor := strings.TrimSpace(req.QueryStringParameters["cursor"]); cursor != "" {
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(cursor)
		if decodeErr != nil {
			return errorResponse(400, "El cursor no es válido."), nil
		}
		input.ExclusiveStartKey = map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: domain.PK(contractID)}, "sk": &ddbtypes.AttributeValueMemberS{Value: string(decoded)}}
	}
	out, err := app.DDB.Query(ctx, input)
	if err != nil {
		return errorResponse(500, "No se pudo listar el historial firmado."), nil
	}
	documents := make([]domain.SignedDocument, 0, len(out.Items))
	for _, raw := range out.Items {
		var item domain.SignedDocumentItem
		if attributevalue.UnmarshalMap(raw, &item) == nil {
			documents = append(documents, item.SignedDocument)
		}
	}
	nextCursor := ""
	if sk, ok := out.LastEvaluatedKey["sk"].(*ddbtypes.AttributeValueMemberS); ok {
		nextCursor = base64.RawURLEncoding.EncodeToString([]byte(sk.Value))
	}
	return lambdautil.SuccessResponse(200, map[string]any{"items": documents, "nextCursor": nextCursor})
}

func HandleSignedDocumentPDF(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	contract, code, err := getItem(ctx, app, req.PathParameters[ContractIDParam])
	if err != nil {
		return errorResponse(code, err.Error()), nil
	}
	if contract.SignedDocument == nil {
		return errorResponse(404, "El contrato no tiene una copia firmada publicada."), nil
	}
	document := contract.SignedDocument
	requestedID := strings.TrimSpace(req.PathParameters[SignedDocumentIDParam])
	if requestedID != "" && requestedID != document.ID {
		found, findErr := findSignedDocument(ctx, app, contract.ID, requestedID)
		if findErr != nil {
			return errorResponse(500, "No se pudo obtener la copia firmada."), nil
		}
		if found == nil {
			return errorResponse(404, "La versión firmada no existe."), nil
		}
		document = found
	}
	if err = app.Documents.VerifyPDF(ctx, document.ObjectKey, document.SHA256, document.Size); err != nil {
		return errorResponse(409, "El PDF firmado no superó la verificación de integridad."), nil
	}
	url, err := app.Documents.PresignPDF(ctx, document.ObjectKey, 15*time.Minute)
	if err != nil {
		return errorResponse(500, "No se pudo obtener el PDF firmado."), nil
	}
	return lambdautil.SuccessResponse(200, map[string]any{"url": url, "expiresAt": time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339)})
}

func getSignedUploadIntent(ctx context.Context, app *App, contractID, id string) (domain.SignedUploadIntent, bool, error) {
	out, err := app.DDB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(app.Config.ProgramsTableName), Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: domain.PK(contractID)}, "sk": &ddbtypes.AttributeValueMemberS{Value: domain.SignedUploadSK(id)}}, ConsistentRead: aws.Bool(true)})
	if err != nil || len(out.Item) == 0 {
		return domain.SignedUploadIntent{}, false, err
	}
	var intent domain.SignedUploadIntent
	err = attributevalue.UnmarshalMap(out.Item, &intent)
	return intent, err == nil, err
}

func findSignedDocument(ctx context.Context, app *App, contractID, id string) (*domain.SignedDocument, error) {
	out, err := app.DDB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(app.Config.ProgramsTableName), Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: domain.PK(contractID)}, "sk": &ddbtypes.AttributeValueMemberS{Value: domain.SignedDocumentSK(id)}}, ConsistentRead: aws.Bool(true)})
	if err != nil || len(out.Item) == 0 {
		return nil, err
	}
	var item domain.SignedDocumentItem
	if err = attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return nil, err
	}
	return &item.SignedDocument, nil
}
