package functions

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"reflect"
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

type CreateAmendmentRequest struct {
	ID                  string                       `json:"id"`
	BaseContractVersion int                          `json:"baseContractVersion"`
	Reason              string                       `json:"reason"`
	After               domain.ContractTermsSnapshot `json:"after"`
}

type ApproveAmendmentRequest struct {
	Version int `json:"version"`
}

type AmendmentPage struct {
	Items      []domain.ContractAmendment `json:"items"`
	NextCursor string                     `json:"nextCursor,omitempty"`
}

type effectiveTermsItem struct {
	PK          string                       `dynamodbav:"pk"`
	SK          string                       `dynamodbav:"sk"`
	Revision    int                          `dynamodbav:"revision"`
	AmendmentID string                       `dynamodbav:"amendmentId"`
	Terms       domain.ContractTermsSnapshot `dynamodbav:"terms"`
	ApprovedAt  string                       `dynamodbav:"approvedAt"`
}

type pendingAmendmentItem struct {
	PK          string `dynamodbav:"pk"`
	SK          string `dynamodbav:"sk"`
	AmendmentID string `dynamodbav:"amendmentId"`
}

func HandleCreateAmendment(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return createAmendment(ctx, req, app)
}

func createAmendment(ctx context.Context, req events.APIGatewayV2HTTPRequest, app *App) (events.APIGatewayV2HTTPResponse, error) {
	actor, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		return errorResponse(http.StatusUnauthorized, "No se pudo identificar al usuario."), nil
	}
	contract, code, err := getItem(ctx, app, req.PathParameters[ContractIDParam])
	if err != nil {
		return errorResponse(code, err.Error()), nil
	}
	var body CreateAmendmentRequest
	if err = lambdautil.BindJSON(req, &body); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	if body.BaseContractVersion != contract.Version {
		return errorResponse(http.StatusConflict, "El contrato base cambió. Recargue antes de crear el anexo."), nil
	}
	before, revision, currentExists, err := getEffectiveTerms(ctx, app, contract)
	if err != nil {
		return errorResponse(500, "No se pudieron obtener las condiciones vigentes."), nil
	}
	amendment, err := domain.NewContractAmendment(body.ID, contract.Contract, before, revision, body.After, body.Reason, actor, time.Now())
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error()), nil
	}
	item := domain.NewContractAmendmentItem(amendment)
	raw, err := attributevalue.MarshalMap(item)
	if err != nil {
		return errorResponse(500, "No se pudo preparar el anexo."), nil
	}
	pendingRaw, err := attributevalue.MarshalMap(pendingAmendmentItem{PK: domain.PK(contract.ID), SK: "AMENDMENT_PENDING", AmendmentID: amendment.ID})
	if err != nil {
		return errorResponse(500, "No se pudo preparar el control del anexo."), nil
	}
	writes := []ddbtypes.TransactWriteItem{
		{ConditionCheck: &ddbtypes.ConditionCheck{TableName: aws.String(app.Config.ProgramsTableName), Key: contractKey(contract.ID), ConditionExpression: aws.String("#status = :approved AND version = :version"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":approved": &ddbtypes.AttributeValueMemberS{Value: string(domain.StatusApproved)}, ":version": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprint(body.BaseContractVersion)}}}},
		{Put: &ddbtypes.Put{TableName: aws.String(app.Config.ProgramsTableName), Item: raw, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &ddbtypes.Put{TableName: aws.String(app.Config.ProgramsTableName), Item: pendingRaw, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}
	writes = append(writes, effectiveTermsCondition(app.Config.ProgramsTableName, contract.ID, revision, currentExists))
	_, err = app.DDBTransactions.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err != nil {
		if persisted, _, readErr := getAmendment(ctx, app, contract.ID, body.ID); readErr == nil && persisted.CreatedBy == actor && persisted.Reason == amendment.Reason && reflect.DeepEqual(persisted.Before, amendment.Before) && reflect.DeepEqual(persisted.After, amendment.After) {
			return lambdautil.SuccessResponse(http.StatusOK, persisted)
		}
		return errorResponse(http.StatusConflict, "El anexo ya existe o el contrato base cambió."), nil
	}
	return lambdautil.SuccessResponse(http.StatusCreated, amendment)
}

func HandleListAmendments(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return listAmendments(ctx, req, app)
}

func listAmendments(ctx context.Context, req events.APIGatewayV2HTTPRequest, app *App) (events.APIGatewayV2HTTPResponse, error) {
	contractID := req.PathParameters[ContractIDParam]
	if _, err := ulidParse(contractID); err != nil {
		return errorResponse(400, err.Error()), nil
	}
	input := &dynamodb.QueryInput{TableName: aws.String(app.Config.ProgramsTableName), KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":pk": &ddbtypes.AttributeValueMemberS{Value: domain.PK(contractID)}, ":prefix": &ddbtypes.AttributeValueMemberS{Value: "AMENDMENT#"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(50)}
	if cursor := strings.TrimSpace(req.QueryStringParameters["cursor"]); cursor != "" {
		if _, parseErr := ulidParse(cursor); parseErr != nil {
			return errorResponse(400, "Cursor de anexo inválido."), nil
		}
		input.ExclusiveStartKey = map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: domain.PK(contractID)}, "sk": &ddbtypes.AttributeValueMemberS{Value: domain.AmendmentSK(cursor)}}
	}
	out, err := app.DDB.Query(ctx, input)
	if err != nil {
		return errorResponse(500, "No se pudieron listar los anexos."), nil
	}
	page := AmendmentPage{Items: make([]domain.ContractAmendment, 0, len(out.Items))}
	for _, raw := range out.Items {
		var item domain.ContractAmendmentItem
		if attributevalue.UnmarshalMap(raw, &item) != nil || item.PK != domain.PK(contractID) || item.SK != domain.AmendmentSK(item.ID) {
			return errorResponse(500, "Existe un anexo con formato inválido."), nil
		}
		page.Items = append(page.Items, item.ContractAmendment)
	}
	if len(out.LastEvaluatedKey) > 0 {
		sk, ok := out.LastEvaluatedKey["sk"].(*ddbtypes.AttributeValueMemberS)
		if !ok || !strings.HasPrefix(sk.Value, "AMENDMENT#") {
			return errorResponse(500, "La paginación de anexos no es válida."), nil
		}
		page.NextCursor = strings.TrimPrefix(sk.Value, "AMENDMENT#")
		if _, parseErr := ulidParse(page.NextCursor); parseErr != nil {
			return errorResponse(500, "La paginación de anexos no es válida."), nil
		}
	}
	return lambdautil.SuccessResponse(200, page)
}

func HandleApproveAmendment(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return approveAmendment(ctx, req, app)
}

func approveAmendment(ctx context.Context, req events.APIGatewayV2HTTPRequest, app *App) (events.APIGatewayV2HTTPResponse, error) {
	actor, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		return errorResponse(http.StatusUnauthorized, "No se pudo identificar al usuario."), nil
	}
	contractID, amendmentID := req.PathParameters[ContractIDParam], req.PathParameters[AmendmentIDParam]
	contract, code, err := getItem(ctx, app, contractID)
	if err != nil {
		return errorResponse(code, err.Error()), nil
	}
	amendment, code, err := getAmendment(ctx, app, contractID, amendmentID)
	if err != nil {
		return errorResponse(code, err.Error()), nil
	}
	var body ApproveAmendmentRequest
	if err = lambdautil.BindJSON(req, &body); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	if amendment.Status == domain.ContractAmendmentApproved {
		return lambdautil.SuccessResponse(200, amendment)
	}
	if body.Version != amendment.Version || amendment.BaseContractVersion != contract.Version {
		return errorResponse(409, "El anexo o su contrato base cambió."), nil
	}
	approved, err := domain.ApproveContractAmendment(amendment, actor, time.Now())
	if err != nil {
		return errorResponse(409, err.Error()), nil
	}
	pdf, err := ComposeAmendmentPDF(contract.Content, approved)
	if err != nil {
		return errorResponse(500, "No se pudo generar el PDF del anexo."), nil
	}
	digest := sha256.Sum256(pdf)
	objectKey := fmt.Sprintf("contracts/%s/amendments/%s/v%d-%x.pdf", contractID, amendmentID, approved.Version, digest)
	if err = app.Documents.PutPDF(ctx, objectKey, pdf); err != nil {
		return errorResponse(500, "No se pudo almacenar el PDF del anexo."), nil
	}
	approved.PDFDocument = &domain.PDFDocument{ObjectKey: objectKey, ContentType: approvedPDFContentType, Size: int64(len(pdf)), SHA256: fmt.Sprintf("%x", digest), GeneratorVersion: amendmentPDFGeneratorVersion, GeneratedAt: approved.ApprovedAt, GeneratedBy: actor}
	raw, err := attributevalue.MarshalMap(domain.NewContractAmendmentItem(approved))
	if err != nil {
		return errorResponse(500, "No se pudo preparar la aprobación del anexo."), nil
	}
	effective := effectiveTermsItem{PK: domain.PK(contractID), SK: "TERMS#CURRENT", Revision: amendment.BaseTermsRevision + 1, AmendmentID: amendment.ID, Terms: approved.After, ApprovedAt: approved.ApprovedAt}
	effectiveRaw, err := attributevalue.MarshalMap(effective)
	if err != nil {
		return errorResponse(500, "No se pudo preparar la vigencia del anexo."), nil
	}
	writes := []ddbtypes.TransactWriteItem{
		{ConditionCheck: &ddbtypes.ConditionCheck{TableName: aws.String(app.Config.ProgramsTableName), Key: contractKey(contractID), ConditionExpression: aws.String("#status = :approved AND version = :baseVersion"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":approved": &ddbtypes.AttributeValueMemberS{Value: string(domain.StatusApproved)}, ":baseVersion": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprint(amendment.BaseContractVersion)}}}},
		{Put: &ddbtypes.Put{TableName: aws.String(app.Config.ProgramsTableName), Item: raw, ConditionExpression: aws.String("#status = :draft AND version = :version"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":draft": &ddbtypes.AttributeValueMemberS{Value: string(domain.ContractAmendmentDraft)}, ":version": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprint(body.Version)}}}},
		{Put: &ddbtypes.Put{TableName: aws.String(app.Config.ProgramsTableName), Item: effectiveRaw, ConditionExpression: aws.String("attribute_not_exists(pk) OR revision = :baseRevision"), ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":baseRevision": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprint(amendment.BaseTermsRevision)}}}},
		{Delete: &ddbtypes.Delete{TableName: aws.String(app.Config.ProgramsTableName), Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: domain.PK(contractID)}, "sk": &ddbtypes.AttributeValueMemberS{Value: "AMENDMENT_PENDING"}}, ConditionExpression: aws.String("amendmentId = :amendmentId"), ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":amendmentId": &ddbtypes.AttributeValueMemberS{Value: amendment.ID}}}},
	}
	if app.Config.PaymentsTableName != "" {
		paymentProjection := effective
		paymentProjection.PK = "TRIP#" + contractID
		paymentRaw, marshalErr := attributevalue.MarshalMap(paymentProjection)
		if marshalErr != nil {
			return errorResponse(500, "No se pudo preparar la vigencia para cobranza."), nil
		}
		writes = append(writes, ddbtypes.TransactWriteItem{Put: &ddbtypes.Put{TableName: aws.String(app.Config.PaymentsTableName), Item: paymentRaw, ConditionExpression: aws.String("attribute_not_exists(pk) OR revision = :baseRevision"), ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":baseRevision": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprint(amendment.BaseTermsRevision)}}}})
	}
	_, err = app.DDBTransactions.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err != nil {
		if persisted, _, readErr := getAmendment(ctx, app, contractID, amendmentID); readErr == nil && persisted.Status == domain.ContractAmendmentApproved && persisted.PDFDocument != nil && persisted.PDFDocument.SHA256 == approved.PDFDocument.SHA256 {
			return lambdautil.SuccessResponse(200, persisted)
		}
		return errorResponse(409, "El anexo cambió o ya fue aprobado."), nil
	}
	return lambdautil.SuccessResponse(200, approved)
}

func HandleApprovedAmendmentPDF(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	return approvedAmendmentPDF(ctx, req, app)
}

func approvedAmendmentPDF(ctx context.Context, req events.APIGatewayV2HTTPRequest, app *App) (events.APIGatewayV2HTTPResponse, error) {
	amendment, code, err := getAmendment(ctx, app, req.PathParameters[ContractIDParam], req.PathParameters[AmendmentIDParam])
	if err != nil {
		return errorResponse(code, err.Error()), nil
	}
	if amendment.Status != domain.ContractAmendmentApproved || amendment.PDFDocument == nil || strings.TrimSpace(amendment.PDFDocument.ObjectKey) == "" {
		return errorResponse(404, "El anexo no tiene un PDF aprobado."), nil
	}
	if err = app.Documents.VerifyPDF(ctx, amendment.PDFDocument.ObjectKey, amendment.PDFDocument.SHA256, amendment.PDFDocument.Size); err != nil {
		return errorResponse(409, "El PDF del anexo no superó la verificación de integridad."), nil
	}
	const validity = 15 * time.Minute
	url, err := app.Documents.PresignPDF(ctx, amendment.PDFDocument.ObjectKey, validity)
	if err != nil {
		return errorResponse(500, "No se pudo obtener el PDF del anexo."), nil
	}
	return lambdautil.SuccessResponse(200, map[string]any{"url": url, "expiresAt": time.Now().UTC().Add(validity).Format(time.RFC3339)})
}

func getAmendment(ctx context.Context, app *App, contractID, amendmentID string) (domain.ContractAmendment, int, error) {
	if _, err := ulidParse(contractID); err != nil {
		return domain.ContractAmendment{}, 400, err
	}
	if _, err := ulidParse(amendmentID); err != nil {
		return domain.ContractAmendment{}, 400, errorsNew("Identificador de anexo inválido.")
	}
	out, err := app.DDB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(app.Config.ProgramsTableName), Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: domain.PK(contractID)}, "sk": &ddbtypes.AttributeValueMemberS{Value: domain.AmendmentSK(amendmentID)}}, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return domain.ContractAmendment{}, 500, errorsNew("No se pudo obtener el anexo.")
	}
	if len(out.Item) == 0 {
		return domain.ContractAmendment{}, 404, errorsNew("Anexo no encontrado.")
	}
	var item domain.ContractAmendmentItem
	if err = attributevalue.UnmarshalMap(out.Item, &item); err != nil || item.ContractID != contractID || item.ID != amendmentID {
		return domain.ContractAmendment{}, 500, errorsNew("El anexo almacenado no es válido.")
	}
	return item.ContractAmendment, 200, nil
}

func contractKey(id string) map[string]ddbtypes.AttributeValue {
	return map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: domain.PK(id)}, "sk": &ddbtypes.AttributeValueMemberS{Value: domain.SK}}
}

func getEffectiveTerms(ctx context.Context, app *App, contract domain.Item) (domain.ContractTermsSnapshot, int, bool, error) {
	out, err := app.DDB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(app.Config.ProgramsTableName), Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: domain.PK(contract.ID)}, "sk": &ddbtypes.AttributeValueMemberS{Value: "TERMS#CURRENT"}}, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return domain.ContractTermsSnapshot{}, 0, false, err
	}
	if len(out.Item) == 0 {
		return domain.TermsSnapshot(contract.Content), 0, false, nil
	}
	var current effectiveTermsItem
	if err = attributevalue.UnmarshalMap(out.Item, &current); err != nil || current.PK != domain.PK(contract.ID) || current.SK != "TERMS#CURRENT" || current.Revision < 1 || current.AmendmentID == "" {
		return domain.ContractTermsSnapshot{}, 0, false, errorsNew("condiciones vigentes inválidas")
	}
	return current.Terms, current.Revision, true, nil
}

func effectiveTermsCondition(table, contractID string, revision int, exists bool) ddbtypes.TransactWriteItem {
	condition := "attribute_not_exists(pk)"
	values := map[string]ddbtypes.AttributeValue(nil)
	if exists {
		condition = "revision = :revision"
		values = map[string]ddbtypes.AttributeValue{":revision": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprint(revision)}}
	}
	return ddbtypes.TransactWriteItem{ConditionCheck: &ddbtypes.ConditionCheck{TableName: aws.String(table), Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: domain.PK(contractID)}, "sk": &ddbtypes.AttributeValueMemberS{Value: "TERMS#CURRENT"}}, ConditionExpression: aws.String(condition), ExpressionAttributeValues: values}}
}
