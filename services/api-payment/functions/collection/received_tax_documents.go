package collection

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/dteinbox"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
)

// ReceivedSyncEvent distingue solicitudes manuales de las ejecuciones programadas.
type ReceivedSyncEvent struct {
	RequestID string `json:"requestId"`
}

// ReceivedTaxDocumentsSyncHandler lee Gmail con el certificado y SSM del emisor.
func ReceivedTaxDocumentsSyncHandler(ctx context.Context, event ReceivedSyncEvent) error {
	stage, err := currentStage()
	if err != nil {
		return err
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return err
	}
	store := dteinbox.Store{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")}
	run := func(ctx context.Context) error {
		return runReceivedTaxDocumentsSync(ctx, cfg, stage, store, event.RequestID != "")
	}
	if event.RequestID != "" {
		if !validPortalID(event.RequestID) {
			return errors.New("identificador de sincronización inválido")
		}
		return dteinbox.ExecuteSyncRequest(ctx, store, event.RequestID, time.Now, run)
	}
	err = run(ctx)
	if errors.Is(err, dteinbox.ErrSyncBusy) {
		return nil
	}
	return err
}

func runReceivedTaxDocumentsSync(ctx context.Context, cfg aws.Config, stage string, store dteinbox.Store, manual bool) error {
	parameters := ssm.NewFromConfig(cfg)
	out, err := parameters.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String("/indomito/" + stage + "/payments/sii/received-gmail"), WithDecryption: aws.Bool(true)})
	if err != nil {
		return err
	}
	var gmailConfig dteinbox.GmailConfig
	if err = json.Unmarshal([]byte(aws.ToString(out.Parameter.Value)), &gmailConfig); err != nil {
		return err
	}
	if !gmailConfig.Enabled {
		if !manual {
			return nil
		}
		return errors.New("lectura Gmail deshabilitada")
	}
	credentials, err := (ssmSIIConfigLoader{Reader: parameters, Stage: stage}).Load(ctx, time.Now())
	if err != nil {
		return err
	}
	worker := dteinbox.Worker{Mail: &dteinbox.Gmail{HTTP: &http.Client{Timeout: 30 * time.Second}}, Store: store, Objects: s3.NewFromConfig(cfg), Bucket: os.Getenv("RECEIPTS_BUCKET_NAME"), Stage: stage, Config: gmailConfig, Credentials: credentials, Now: time.Now}
	client := sii.Client{HTTP: &http.Client{Timeout: 6 * time.Second}, Now: time.Now}
	worker.Registry = func(ctx context.Context, document dteinbox.Document) (sii.ReceivedResult, error) {
		return client.ReceivedRegistry(ctx, credentials, document.IssuerRUT, document.Kind, document.Folio, "")
	}
	return worker.Run(ctx)
}

var receivedIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ReceivedTaxDocumentsHandler lista originales y registra decisiones explícitas en el SII.
func ReceivedTaxDocumentsHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	actor, denied := annexAdminIdentity(req)
	if denied != nil {
		return *denied, nil
	}
	stage, err := currentStage()
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	store := dteinbox.Store{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")}
	month := req.QueryStringParameters["month"]
	if _, err = time.Parse("2006-01", month); err != nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	reply := func(value any) (events.APIGatewayV2HTTPResponse, error) {
		return lambdautil.SuccessResponseWithHeaders(200, value, map[string]string{"cache-control": "no-store"})
	}
	id := req.PathParameters["documentId"]
	method := req.RequestContext.HTTP.Method
	if id == "" && method == "GET" {
		cursor := req.QueryStringParameters["cursor"]
		if cursor != "" && !receivedIDPattern.MatchString(cursor) {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		items, next, e := store.List(ctx, month, cursor)
		if e != nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		state, e := store.State(ctx)
		if e != nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		issues, e := store.Issues(ctx)
		if e != nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		manual, e := store.LatestSyncRequest(ctx, time.Now())
		if e != nil {
			return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		return reply(map[string]any{"items": items, "nextCursor": next, "sync": state, "issues": issues, "manualSync": manual})
	}
	if !receivedIDPattern.MatchString(id) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	document, err := store.Get(ctx, month, id)
	if err != nil {
		return adminFailure(req, 404, "NOT_FOUND")
	}
	if method == "GET" && strings.HasSuffix(req.RawPath, "/descarga") {
		key := document.XMLKey
		if !strings.HasPrefix(key, "tax-documents/received/xml/") {
			return adminFailure(req, 409, "PAYMENT_STATE_CONFLICT")
		}
		signed, e := s3.NewPresignClient(s3.NewFromConfig(cfg)).PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(os.Getenv("RECEIPTS_BUCKET_NAME")), Key: &key, ResponseContentType: aws.String("application/xml"), ResponseContentDisposition: aws.String(`attachment; filename="dte.xml"`)}, func(o *s3.PresignOptions) { o.Expires = 2 * time.Minute })
		if e != nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		return reply(map[string]any{"url": signed.URL})
	}
	if method != "POST" {
		return adminFailure(req, 405, "METHOD_NOT_ALLOWED")
	}
	var input struct {
		CommandID string `json:"commandId"`
		Action    string `json:"action"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decodeAdmin(req, &input) != nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if input.Action != "" {
		if !validPortalID(input.CommandID) || !input.Confirmed || len(strings.TrimSpace(input.Reason)) < 5 || len(input.Reason) > 500 {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
		switch input.Action {
		case "ACD", "ERM", "RCD", "RFP", "RFT":
		default:
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
	}
	parameters := ssm.NewFromConfig(cfg)
	credentials, err := (ssmSIIConfigLoader{Reader: parameters, Stage: stage}).Load(ctx, time.Now())
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	if (stage == "prd") != (credentials.Environment == sii.EnvironmentProduction) || !strings.EqualFold(document.ReceiverRUT, credentials.IssuerRUT) {
		return adminFailure(req, 409, "PAYMENT_STATE_CONFLICT")
	}
	client := sii.Client{HTTP: &http.Client{Timeout: 6 * time.Second}, Now: time.Now}
	if input.Action != "" {
		if restriction := document.RegistryRestriction(); restriction != "" {
			code := 27
			return reply(map[string]any{"document": document, "result": sii.ReceivedResult{Code: &code, Description: restriction, Events: []sii.ReceivedEvent{}}, "successful": false})
		}
		prior, e := client.ReceivedRegistry(ctx, credentials, document.IssuerRUT, document.Kind, document.Folio, "")
		if e == nil && prior.Code != nil && *prior.Code == 27 {
			document.Registry = &prior
			document.LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
			if err = store.Update(ctx, document); err != nil {
				return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
			}
			return reply(map[string]any{"document": document, "result": prior, "successful": false})
		}
		if e != nil || prior.Code == nil || (*prior.Code != 15 && *prior.Code != 16) {
			return adminFailure(req, 409, "PAYMENT_STATE_CONFLICT")
		}
		for _, event := range prior.Events {
			if (input.Action == "ACD" || input.Action == "ERM") && (event.Code == "RCD" || event.Code == "RFP" || event.Code == "RFT") {
				return adminFailure(req, 409, "PAYMENT_STATE_CONFLICT")
			}
			if strings.HasPrefix(input.Action, "R") && (event.Code == "ACD" || event.Code == "ERM") {
				return adminFailure(req, 409, "PAYMENT_STATE_CONFLICT")
			}
		}
		if err = store.ClaimAction(ctx, id, input.CommandID, actor, input.Action, input.Reason); err != nil {
			return adminFailure(req, 409, "PAYMENT_STATE_CONFLICT")
		}
	}
	result, err := client.ReceivedRegistry(ctx, credentials, document.IssuerRUT, document.Kind, document.Folio, input.Action)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	if input.Action != "" {
		if err = store.FinishAction(ctx, id, input.CommandID, actor, input.Action, input.Reason, result); err != nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
	}
	if input.Action == "" {
		document.Registry = &result
		document.LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
		if err = store.Update(ctx, document); err != nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
	}
	return reply(map[string]any{"document": document, "result": result, "successful": result.Code != nil && (*result.Code == 0 || *result.Code == 7)})
}
