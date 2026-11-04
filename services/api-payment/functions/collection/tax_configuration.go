package collection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/mail"
	"os"
	"strings"
	"time"

	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type taxConfigurationInput struct {
	SenderRUT             string          `json:"senderRut"`
	IssuerName            string          `json:"issuerName"`
	IssuerActivity        string          `json:"issuerActivity"`
	IssuerAddress         string          `json:"issuerAddress"`
	IssuerCommune         string          `json:"issuerCommune"`
	NotificationEmail     string          `json:"notificationEmail"`
	Environment           sii.Environment `json:"environment"`
	ResolutionDate        string          `json:"resolutionDate"`
	ResolutionNumber      int64           `json:"resolutionNumber"`
	Enabled               bool            `json:"enabled"`
	CertificationApproved bool            `json:"certificationApproved"`
}

type taxCAFUpload struct {
	CAFXMLBase64 string `json:"cafXmlBase64"`
}

type taxPFXUpload struct {
	PFXBase64 string `json:"pfxBase64"`
	Password  string `json:"password"`
}

// TaxConfigurationHandler administra exactamente la configuración SII activa de boletas.
// El resumen nunca expone el CAF, PFX ni la contraseña.
func TaxConfigurationHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	stage, err := currentStage()
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	if req.RequestContext.HTTP.Method != "GET" && req.RequestContext.HTTP.Method != "PUT" && req.RequestContext.HTTP.Method != "POST" {
		return adminFailure(req, 405, "METHOD_NOT_ALLOWED")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	app := taxConfigurationApp{
		payments:      dynamodb.NewFromConfig(cfg),
		paymentsTable: strings.TrimSpace(os.Getenv("PAYMENTS_TABLE_NAME")),
		parameters:    ssm.NewFromConfig(cfg),
		stage:         stage,
		now:           time.Now,
	}
	return app.handle(ctx, req)
}

type taxConfigurationApp struct {
	payments      *dynamodb.Client
	paymentsTable string
	parameters    siiParameterAPI
	stage         string
	now           func() time.Time
}

func (a taxConfigurationApp) handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	switch req.RequestContext.HTTP.Method {
	case "GET":
		return a.summary(ctx, req)
	case "PUT":
		return a.update(ctx, req)
	case "POST":
		if strings.HasSuffix(req.RawPath, "/caf") {
			return a.uploadCAF(ctx, req)
		}
		if strings.HasSuffix(req.RawPath, "/pfx") {
			return a.uploadPFX(ctx, req)
		}
	}
	return adminFailure(req, 404, "NOT_FOUND")
}

func (a taxConfigurationApp) load(ctx context.Context) (sii.Config, sii.Credentials, error) {
	if a.parameters == nil || a.now == nil {
		return sii.Config{}, sii.Credentials{}, sii.ErrInvalidConfiguration
	}
	raw, err := (siiParameterStore{Client: a.parameters, Stage: a.stage}).Load(ctx)
	if err != nil {
		return sii.Config{}, sii.Credentials{}, sii.ErrInvalidConfiguration
	}
	var config sii.Config
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil {
		return sii.Config{}, sii.Credentials{}, sii.ErrInvalidConfiguration
	}
	credentials, err := sii.ParseConfig(raw, a.now().UTC())
	if err != nil {
		return sii.Config{}, sii.Credentials{}, err
	}
	return config, credentials, nil
}

func (a taxConfigurationApp) loadConfig(ctx context.Context) (sii.Config, bool, error) {
	if a.parameters == nil {
		return sii.Config{}, false, sii.ErrInvalidConfiguration
	}
	raw, err := (siiParameterStore{Client: a.parameters, Stage: a.stage}).Load(ctx)
	if err != nil {
		if errors.Is(err, errSIIParameterMissing) {
			return sii.Config{}, false, nil
		}
		return sii.Config{}, false, err
	}
	var config sii.Config
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil {
		return sii.Config{}, false, sii.ErrInvalidConfiguration
	}
	return config, true, nil
}

func (a taxConfigurationApp) summary(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	config, _, err := a.loadConfig(ctx)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	caf := map[string]any{"from": int64(0), "to": int64(0), "used": int64(0), "available": int64(0), "next": nil, "usagePercentage": int64(0), "authorizationDate": "", "severity": "MISSING"}
	certificate := map[string]any{"subject": "", "validFrom": "", "validTo": "", "daysUntilExpiry": int64(0), "severity": "MISSING"}
	var cafInfo sii.CAFInfo
	if rawCAF, decodeErr := base64.StdEncoding.DecodeString(config.CAFXMLBase64); decodeErr == nil && len(rawCAF) > 0 {
		cafInfo, err = sii.ValidateCAF(rawCAF)
		if err != nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		lastReserved, reserveErr := a.lastReservedFolio(ctx)
		if reserveErr != nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		used := int64(0)
		if lastReserved >= cafInfo.From {
			used = lastReserved - cafInfo.From + 1
		}
		available := cafInfo.To - cafInfo.From + 1 - used
		if available < 0 {
			available = 0
		}
		var next any
		if available > 0 {
			next = cafInfo.From
			if lastReserved >= cafInfo.From {
				next = lastReserved + 1
			}
		}
		total := cafInfo.To - cafInfo.From + 1
		percentage := int64(0)
		if total > 0 {
			percentage = used * 100 / total
		}
		severity := "OK"
		if available == 0 {
			severity = "EXHAUSTED"
		} else if available <= 20 {
			severity = "LOW"
		}
		caf = map[string]any{"from": cafInfo.From, "to": cafInfo.To, "used": used, "available": available, "next": next, "usagePercentage": percentage, "authorizationDate": cafInfo.AuthorizationAt, "severity": severity}
	}
	if rawPFX, decodeErr := base64.StdEncoding.DecodeString(config.CertificatePFXBase64); decodeErr == nil && len(rawPFX) > 0 {
		cert, certErr := sii.ValidatePFX(rawPFX, config.CertificatePassword, a.now().UTC())
		if certErr != nil {
			return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
		}
		days := int64(cert.NotAfter.Sub(a.now().UTC()).Hours() / 24)
		severity := "OK"
		if days < 0 {
			severity = "EXPIRED"
		} else if days <= 30 {
			severity = "EXPIRING"
		}
		certificate = map[string]any{"subject": cert.Subject.String(), "validFrom": cert.NotBefore.UTC().Format(time.RFC3339), "validTo": cert.NotAfter.UTC().Format(time.RFC3339), "daysUntilExpiry": days, "severity": severity}
	}
	return lambdautil.SuccessResponseWithHeaders(200, map[string]any{
		"senderRut": config.SenderRUT, "issuerName": cafInfo.IssuerName,
		"issuerActivity": config.IssuerActivity, "issuerAddress": config.IssuerAddress,
		"issuerCommune": config.IssuerCommune, "notificationEmail": config.NotificationEmail, "environment": config.Environment,
		"resolutionDate": config.ResolutionDate, "resolutionNumber": config.ResolutionNumber,
		"enabled": config.Enabled, "certificationApproved": false,
		"caf": caf, "certificate": certificate,
	}, map[string]string{"cache-control": "no-store"})
}

func (a taxConfigurationApp) lastReservedFolio(ctx context.Context) (int64, error) {
	if a.payments == nil || a.paymentsTable == "" {
		return 0, nil
	}
	result, err := a.payments.GetItem(ctx, &dynamodb.GetItemInput{TableName: &a.paymentsTable, Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: "TAX_FOLIO#39"}, "sk": &ddbtypes.AttributeValueMemberS{Value: "META"}}, ConsistentRead: aws.Bool(true)})
	if err != nil || len(result.Item) == 0 {
		return 0, err
	}
	var item struct {
		Count int64 `dynamodbav:"count"`
	}
	if attributevalue.UnmarshalMap(result.Item, &item) != nil {
		return 0, sii.ErrInvalidConfiguration
	}
	return item.Count, nil
}

func (a taxConfigurationApp) save(ctx context.Context, config sii.Config) error {
	raw, err := json.Marshal(config)
	if err != nil || a.parameters == nil {
		return sii.ErrInvalidConfiguration
	}
	return (siiParameterStore{Client: a.parameters, Stage: a.stage}).Save(ctx, raw)
}

func (a taxConfigurationApp) update(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var input taxConfigurationInput
	if decodeAdmin(req, &input) != nil || (input.Environment != sii.EnvironmentCertification && input.Environment != sii.EnvironmentProduction) || strings.TrimSpace(input.IssuerName) == "" || !validSIINotificationEmail(input.NotificationEmail) {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	config, _, err := a.loadConfig(ctx)
	if err != nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if rawCAF, decodeErr := base64.StdEncoding.DecodeString(config.CAFXMLBase64); decodeErr == nil && len(rawCAF) > 0 {
		info, validateErr := sii.ValidateCAF(rawCAF)
		if validateErr != nil || input.IssuerName != info.IssuerName {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
	}
	config.SenderRUT = strings.TrimSpace(input.SenderRUT)
	config.IssuerActivity = strings.TrimSpace(input.IssuerActivity)
	config.IssuerAddress = strings.TrimSpace(input.IssuerAddress)
	config.IssuerCommune = strings.TrimSpace(input.IssuerCommune)
	config.NotificationEmail = strings.TrimSpace(input.NotificationEmail)
	config.Environment = input.Environment
	config.ResolutionDate = input.ResolutionDate
	config.ResolutionNumber = input.ResolutionNumber
	config.Enabled = input.Enabled
	raw, marshalErr := json.Marshal(config)
	if marshalErr != nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if input.Enabled {
		if _, err := sii.ParseConfig(raw, a.now().UTC()); err != nil {
			return adminFailure(req, 400, "INVALID_REQUEST")
		}
	}
	if err = a.save(ctx, config); err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return a.summary(ctx, req)
}

func validSIINotificationEmail(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && len(value) <= 254
}

func (a taxConfigurationApp) uploadCAF(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var input taxCAFUpload
	if decodeAdmin(req, &input) != nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	raw, err := base64.StdEncoding.DecodeString(input.CAFXMLBase64)
	if err != nil || len(raw) == 0 || len(raw) > 512*1024 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	info, err := sii.ValidateCAF(raw)
	if err != nil {
		return adminFailure(req, 400, "INVALID_CAF")
	}
	config, _, err := a.loadConfig(ctx)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	lastReserved, err := a.lastReservedFolio(ctx)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	var currentFrom, currentTo int64
	if currentRaw, decodeErr := base64.StdEncoding.DecodeString(config.CAFXMLBase64); decodeErr == nil && len(currentRaw) > 0 {
		if currentInfo, validateErr := sii.ValidateCAF(currentRaw); validateErr == nil {
			currentFrom, currentTo = currentInfo.From, currentInfo.To
		}
	}
	if (strings.TrimSpace(config.SenderRUT) != "" && info.IssuerRUT != strings.TrimSpace(config.SenderRUT)) || info.To <= lastReserved || (lastReserved >= currentFrom && lastReserved < currentTo) {
		return adminFailure(req, 409, "CAF_RANGE_CONFLICT")
	}
	config.CAFXMLBase64 = base64.StdEncoding.EncodeToString(raw)
	if err = a.save(ctx, config); err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return a.summary(ctx, req)
}

func (a taxConfigurationApp) uploadPFX(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var input taxPFXUpload
	if decodeAdmin(req, &input) != nil {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	raw, err := base64.StdEncoding.DecodeString(input.PFXBase64)
	if err != nil || len(raw) == 0 || len(raw) > 512*1024 {
		return adminFailure(req, 400, "INVALID_REQUEST")
	}
	if _, err = sii.ValidatePFX(raw, input.Password, a.now().UTC()); err != nil {
		return adminFailure(req, 400, "INVALID_CERTIFICATE")
	}
	config, _, err := a.loadConfig(ctx)
	if err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	config.CertificatePFXBase64 = base64.StdEncoding.EncodeToString(raw)
	config.CertificatePassword = input.Password
	if err = a.save(ctx, config); err != nil {
		return adminFailure(req, 503, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return a.summary(ctx, req)
}

func mustJSON(value sii.Config) []byte {
	raw, _ := json.Marshal(value)
	return raw
}
