package collection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

// RenditionApp separa autenticación externa de aplicación financiera recuperable.
type RenditionApp struct {
	Accounts Service
	Objects  RenditionObjects
	Verifier providers.RenditionVerifier
	Policy   RenditionPolicy
	Stage    string
	Now      func() time.Time
}

// Handle recibe únicamente mensajes firmados. El mismo receptor permite reenvío
// de evidencia original; no acepta JSON sin autenticar ni dispara caja en HTTP.
func (a RenditionApp) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if a.Now == nil || a.Objects == nil {
		return webhookResponse(503)
	}
	if req.RequestContext.HTTP.Method != "POST" {
		return webhookResponse(405)
	}
	if len(req.Body) > providers.MaxRenditionBytes*4/3+8 {
		return webhookResponse(413)
	}
	raw := []byte(req.Body)
	if req.IsBase64Encoded {
		var err error
		raw, err = base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return webhookResponse(400)
		}
	}
	report, err := a.Verifier.Verify(raw, a.Now())
	if err != nil {
		return webhookResponse(400)
	}
	_, err = a.Accounts.ReceiveRendition(ctx, a.Objects, a.Stage, report, raw, a.Policy, a.Now())
	if errors.Is(err, ErrReplayMismatch) {
		return webhookResponse(409)
	}
	if err != nil {
		return webhookResponse(503)
	}
	return webhookResponse(200)
}

// Run procesa trabajos vencidos por Query; la rotación de fecha evita starvation.
// Un fallo conserva el trabajo. No depende de la retención de DynamoDB Streams.
func (a RenditionApp) Run(ctx context.Context) error {
	db, ok := a.Accounts.DB.(queryDatabase)
	if !ok {
		return errors.New("query no disponible")
	}
	input := renditionQuery("RENDITION_JOBS", "")
	input.TableName = &a.Accounts.Table
	input.KeyConditionExpression = aws.String("pk = :pk AND sk BETWEEN :from AND :to")
	delete(input.ExpressionAttributeValues, ":prefix")
	input.ExpressionAttributeValues[":from"] = &types.AttributeValueMemberS{Value: "0000"}
	input.ExpressionAttributeValues[":to"] = &types.AttributeValueMemberS{Value: a.Now().UTC().Format(time.RFC3339) + "~"}
	input.Limit = aws.Int32(10)
	page, err := db.Query(ctx, input)
	if err != nil {
		return err
	}
	var failures []error
	for _, raw := range page.Items {
		if err = ctx.Err(); err != nil {
			return err
		}
		var job record
		if attributevalue.UnmarshalMap(raw, &job) != nil || !validPortalID(job.AccountID) {
			return errors.New("trabajo de rendicion invalido")
		}
		// La configuración vigente funciona como interruptor de aplicación. No
		// continúa aplicando un lote si se revocó o cambió su política validada.
		row, workErr := a.Accounts.read(ctx, renditionPK(job.AccountID), "META")
		if workErr == nil && row.Rendition != nil && row.Rendition.Policy.Mode == "APPLY" && (a.Policy.Mode != "APPLY" || a.Policy.Revision != row.Rendition.Policy.Revision) {
			workErr = providers.ErrRenditionPolicy
		}
		if workErr == nil {
			workErr = a.Accounts.ProcessRendition(ctx, a.Objects, job.AccountID, a.Now(), 50)
		}
		if err = a.Accounts.RescheduleRendition(ctx, job.AccountID, job.SK, a.Now(), workErr != nil); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// HandleAdmin consulta evidencia resumida o reanuda un lote; nunca expone cuentas
// bancarias, documentos originales ni información personal de pasajeros.
func (a RenditionApp) HandleAdmin(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	id := req.PathParameters["id"]
	if id != "" && !validPortalID(id) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if req.RequestContext.HTTP.Method == "POST" {
		if id == "" {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		if err := a.Accounts.ReplayRendition(ctx, id, a.Policy, actor, a.Now()); err != nil {
			return domainFailure(req, err)
		}
		return lambdautil.SuccessResponseWithHeaders(200, map[string]string{"status": "PENDING"}, map[string]string{"cache-control": "no-store"})
	}
	if req.RequestContext.HTTP.Method != "GET" {
		return adminFailure(req, 405, "INVALID_REQUEST")
	}
	db, ok := a.Accounts.DB.(queryDatabase)
	if !ok {
		return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
	}
	var state *RenditionState
	input := renditionQuery("", "")
	input.TableName = &a.Accounts.Table
	if id != "" {
		row, err := a.Accounts.read(ctx, renditionPK(id), "META")
		if err != nil {
			return domainFailure(req, err)
		}
		state = row.Rendition
		input = renditionQuery(renditionPK(id), "ITEM#")
		input.TableName = &a.Accounts.Table
	} else {
		month := req.QueryStringParameters["month"]
		if _, err := time.Parse("2006-01", month); err != nil {
			return adminFailure(req, 400, "INVALID_MONTH")
		}
		input = renditionQuery("RENDITION_MONTH#"+month, "")
		input.TableName = &a.Accounts.Table
	}
	if cursor := req.QueryStringParameters["cursor"]; cursor != "" {
		if len(cursor) > 160 {
			return adminFailure(req, 400, "INVALID_CURSOR")
		}
		input.ExclusiveStartKey = key(input.ExpressionAttributeValues[":pk"].(*types.AttributeValueMemberS).Value, cursor)
	}
	page, err := db.Query(ctx, input)
	if err != nil {
		return domainFailure(req, err)
	}
	items := []any{}
	for _, raw := range page.Items {
		var r record
		if attributevalue.UnmarshalMap(raw, &r) != nil {
			return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
		}
		if id != "" {
			items = append(items, r.RenditionResult)
		} else {
			items = append(items, r.Rendition)
		}
	}
	next := ""
	if sk, ok := page.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
		next = sk.Value
	}
	return lambdautil.SuccessResponseWithHeaders(200, struct {
		Rendition  *RenditionState `json:"rendition,omitempty"`
		Items      []any           `json:"items"`
		NextCursor string          `json:"nextCursor,omitempty"`
	}{state, items, next}, map[string]string{"cache-control": "no-store"})
}

// decodeRenditionConfig no permite habilitación accidental por valores vacíos.
func decodeRenditionConfig(b []byte) (renditionConfig, error) {
	var c renditionConfig
	if json.Unmarshal(b, &c) != nil {
		return c, providers.ErrRenditionPolicy
	}
	if c.Policy.Mode == "" {
		c.Policy.Mode = "OBSERVE"
	}
	if c.RecipientUID == "" || c.Bank == "" || c.Account == "" || c.Policy.validate() != nil {
		return c, providers.ErrRenditionPolicy
	}
	return c, nil
}
