package functions

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-contract/domain"
)

type CreateRequest struct {
	ProgramID        string                   `json:"programId"`
	ProgramReference *domain.ProgramReference `json:"programReference"`
	Period           string                   `json:"period"`
	Content          domain.Content           `json:"content"`
}
type UpdateRequest struct {
	ProgramID        string                   `json:"programId"`
	ProgramReference *domain.ProgramReference `json:"programReference"`
	Period           string                   `json:"period"`
	Status           domain.Status            `json:"status"`
	Content          domain.Content           `json:"content"`
	Version          int                      `json:"version"`
}
type PDFRequest struct {
	Content domain.Content `json:"content"`
	Preview bool           `json:"preview"`
}

func register(route Route, handler func(context.Context, events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error)) RegisterFunc {
	return func(e *echo.Echo, _ *App, _ *zap.Logger) { route.Register(e, lambdautil.EchoAdapter(handler)) }
}

var RegisterCreate = register(CreateRoute, HandleCreate)
var RegisterList = register(ListRoute, HandleList)
var RegisterGet = register(GetRoute, HandleGet)
var RegisterUpdate = register(UpdateRoute, HandleUpdate)
var RegisterPDF = register(PDFRoute, HandlePDF)

func HandleCreate(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	var body CreateRequest
	if err := lambdautil.BindJSON(req, &body); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	item, err := domain.NewItem(domain.NewID(), body.ProgramID, body.ProgramReference, body.Period, body.Content, time.Now())
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error()), nil
	}
	raw, err := attributevalue.MarshalMap(item)
	if err != nil {
		return errorResponse(500, err.Error()), nil
	}
	_, err = app.DDB.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(app.Config.ProgramsTableName), Item: raw, ConditionExpression: aws.String("attribute_not_exists(pk)")})
	if err != nil {
		return errorResponse(500, "No se pudo guardar el contrato."), nil
	}
	return lambdautil.SuccessResponse(http.StatusCreated, item.Contract)
}

func HandleGet(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	item, code, err := getItem(ctx, app, req.PathParameters[ContractIDParam])
	if err != nil {
		return errorResponse(code, err.Error()), nil
	}
	return lambdautil.SuccessResponse(200, item.Contract)
}

func HandleList(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	year := strings.TrimSpace(req.QueryStringParameters["year"])
	if len(year) != 4 {
		return errorResponse(400, "El año es obligatorio y debe usar YYYY."), nil
	}
	out, err := app.DDB.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(app.Config.ProgramsTableName), IndexName: aws.String("gsi-periodo-index"), KeyConditionExpression: aws.String("gsiPeriodPk = :pk"), ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":pk": &ddbtypes.AttributeValueMemberS{Value: domain.YearPK(year)}}, ScanIndexForward: aws.Bool(false)})
	if err != nil {
		return errorResponse(500, "No se pudieron listar los contratos."), nil
	}
	contracts := make([]domain.Contract, 0, len(out.Items))
	for _, raw := range out.Items {
		var item domain.Item
		if attributevalue.UnmarshalMap(raw, &item) == nil {
			contracts = append(contracts, item.Contract)
		}
	}
	sort.SliceStable(contracts, func(i, j int) bool { return contracts[i].CreatedAt > contracts[j].CreatedAt })
	return lambdautil.SuccessResponse(200, contracts)
}

func HandleUpdate(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := GetApp(ctx)
	if err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	id := req.PathParameters[ContractIDParam]
	current, code, err := getItem(ctx, app, id)
	if err != nil {
		return errorResponse(code, err.Error()), nil
	}
	var body UpdateRequest
	if err := lambdautil.BindJSON(req, &body); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	domain.NormalizePayments(&body.Content)
	if err := domain.NormalizeDates(&body.Content, body.ProgramReference); err != nil {
		return errorResponse(400, err.Error()), nil
	}
	if current.Status == domain.StatusApproved {
		return errorResponse(409, "El contrato aprobado es inmutable."), nil
	}
	if err := domain.ValidateContent(body.Content); err != nil {
		return errorResponse(400, err.Error()), nil
	}
	if body.Status == "" {
		body.Status = current.Status
	}
	if !body.Status.Valid() || !domain.CanTransition(current.Status, body.Status) {
		return errorResponse(409, "La transicion de estado no esta permitida."), nil
	}
	if body.Version != current.Version {
		return errorResponse(409, "El contrato fue modificado por otro usuario. Recargue antes de guardar."), nil
	}
	period := body.Period
	if period == "" {
		period = current.Period
	}
	if body.ProgramReference != nil && strings.TrimSpace(body.ProgramReference.ID) != strings.TrimSpace(body.ProgramID) {
		return errorResponse(400, "La referencia del programa no coincide con programId."), nil
	}
	current.Content = body.Content
	current.ProgramID = strings.TrimSpace(body.ProgramID)
	current.ProgramReference = body.ProgramReference
	current.Status = body.Status
	current.Period = period
	current.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	current.Version++
	current.GSIPeriodPK = domain.YearPK(period)
	current.GSIPeriodSK = current.CreatedAt + "#" + id
	raw, err := attributevalue.MarshalMap(current)
	if err != nil {
		return errorResponse(500, err.Error()), nil
	}
	_, err = app.DDB.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(app.Config.ProgramsTableName), Item: raw, ConditionExpression: aws.String("#status <> :approved AND version = :version"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":approved": &ddbtypes.AttributeValueMemberS{Value: string(domain.StatusApproved)}, ":version": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprint(body.Version)}}})
	if err != nil {
		return errorResponse(409, "El contrato cambio o ya fue aprobado."), nil
	}
	return lambdautil.SuccessResponse(200, current.Contract)
}

func HandlePDF(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var body PDFRequest
	if err := lambdautil.BindJSON(req, &body); err != nil {
		return lambdautil.ErrorResponse(req, err)
	}
	domain.NormalizePayments(&body.Content)
	if err := domain.NormalizeDates(&body.Content, nil); err != nil {
		return errorResponse(400, err.Error()), nil
	}
	if err := domain.ValidateContent(body.Content); err != nil {
		return errorResponse(400, err.Error()), nil
	}
	pdf, err := ComposePDF(body.Content, body.Preview)
	if err != nil {
		return errorResponse(500, "No se pudo generar el PDF."), nil
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: 200, Headers: map[string]string{"content-type": "application/pdf", "content-disposition": "inline; filename=contrato-prestacion-servicios.pdf", "cache-control": "no-store"}, Body: base64.StdEncoding.EncodeToString(pdf), IsBase64Encoded: true}, nil
}

func getItem(ctx context.Context, app *App, id string) (domain.Item, int, error) {
	if _, err := ulidParse(id); err != nil {
		return domain.Item{}, 400, err
	}
	out, err := app.DDB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(app.Config.ProgramsTableName), Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: domain.PK(id)}, "sk": &ddbtypes.AttributeValueMemberS{Value: domain.SK}}, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return domain.Item{}, 500, errorsNew("No se pudo obtener el contrato.")
	}
	if len(out.Item) == 0 {
		return domain.Item{}, 404, errorsNew("Contrato no encontrado.")
	}
	var item domain.Item
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return domain.Item{}, 500, err
	}
	return item, 200, nil
}
func ulidParse(id string) (string, error) {
	if strings.TrimSpace(id) == "" || len(id) != 26 {
		return "", errorsNew("Identificador de contrato invalido.")
	}
	return id, nil
}
func errorsNew(message string) error { return fmt.Errorf("%s", message) }
func errorResponse(code int, message string) events.APIGatewayV2HTTPResponse {
	return events.APIGatewayV2HTTPResponse{StatusCode: code, Headers: map[string]string{"content-type": "application/json"}, Body: fmt.Sprintf(`{"message":%q}`, message)}
}
