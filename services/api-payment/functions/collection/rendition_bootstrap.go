package collection

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

type renditionConfig struct {
	RecipientUID   string          `json:"recipientUid"`
	Bank           string          `json:"bank"`
	Account        string          `json:"account"`
	CertificatePEM string          `json:"certificatePem"`
	Policy         RenditionPolicy `json:"policy"`
}

type renditionS3 struct {
	client *s3.Client
	bucket string
}

func (s renditionS3) Put(ctx context.Context, key string, b []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: bytes.NewReader(b), ContentType: aws.String("application/octet-stream")})
	return err
}
func (s renditionS3) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(out.Body, providers.MaxRenditionBytes+1))
	closeErr := out.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(b) > providers.MaxRenditionBytes {
		return nil, providers.ErrVerification
	}
	return b, nil
}

func getRenditionApp(ctx context.Context) (*RenditionApp, error) {
	stage, err := currentStage()
	if err != nil {
		return nil, err
	}
	table, bucket := os.Getenv("PAYMENTS_TABLE_NAME"), os.Getenv("RECEIPTS_BUCKET_NAME")
	if table == "" || bucket == "" {
		return nil, errors.New("configuración de rendiciones incompleta")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return nil, err
	}
	name := "/indomito/" + stage + "/payments/khipu/renditions/config"
	out, err := ssm.NewFromConfig(cfg).GetParameter(ctx, &ssm.GetParameterInput{Name: &name, WithDecryption: aws.Bool(true)})
	if err != nil {
		return nil, err
	}
	if out.Parameter == nil || out.Parameter.Type != types.ParameterTypeSecureString || out.Parameter.Value == nil {
		return nil, errors.New("configuración requiere SecureString")
	}
	c, err := decodeRenditionConfig([]byte(*out.Parameter.Value))
	if err != nil {
		return nil, err
	}
	v := providers.RenditionVerifier{RecipientUID: c.RecipientUID, Bank: c.Bank, Account: c.Account}
	if c.CertificatePEM != "" {
		block, rest := pem.Decode([]byte(c.CertificatePEM))
		if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
			return nil, providers.ErrVerification
		}
		v.Pinned, err = x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
	}
	return &RenditionApp{Accounts: Service{DB: dynamodb.NewFromConfig(cfg), Table: table}, Objects: renditionS3{client: s3.NewFromConfig(cfg), bucket: bucket}, Verifier: v, Policy: c.Policy, Stage: stage, Now: time.Now}, nil
}

// RenditionWebhookHandler recibe avisos externos sin credenciales administrativas.
func RenditionWebhookHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.RequestContext.HTTP.Method != "POST" {
		return webhookResponse(405), nil
	}
	if len(req.Body) > providers.MaxRenditionBytes*4/3+8 {
		return webhookResponse(413), nil
	}
	a, err := getRenditionApp(ctx)
	if err != nil {
		return webhookResponse(503), nil
	}
	return a.Handle(ctx, req), nil
}

// RenditionWorkerHandler ejecuta recuperación programada independiente del webhook.
func RenditionWorkerHandler(ctx context.Context) error {
	a, err := getRenditionApp(ctx)
	if err != nil {
		// Un ambiente sin configuración aún no tiene habilitada la integración.
		var missing *types.ParameterNotFound
		if errors.As(err, &missing) {
			return nil
		}
		return err
	}
	return a.Run(ctx)
}

// RenditionAdminHandler aplica la autorización administrativa antes de leer configuración.
func RenditionAdminHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if _, denied := annexAdminIdentity(req); denied != nil {
		return *denied, nil
	}
	a, err := getRenditionApp(ctx)
	if err != nil {
		return adminFailure(req, 503, "SERVICE_UNAVAILABLE")
	}
	return a.HandleAdmin(ctx, req)
}
