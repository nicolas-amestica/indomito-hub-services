package renditionbootstrap

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
	"go.uber.org/zap"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

type captureConfig struct {
	Enabled        bool      `json:"enabled"`
	Until          time.Time `json:"until"`
	CertificatePEM string    `json:"certificatePem"`
}

type objectClient interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

type objects struct {
	client objectClient
	bucket string
}

func (s objects) Save(ctx context.Context, key, digest string, body []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &s.bucket, Key: &key, Body: bytes.NewReader(body),
		IfNoneMatch: aws.String("*"), ContentType: aws.String("application/json"),
		ServerSideEncryption: s3types.ServerSideEncryptionAes256,
		Metadata:             map[string]string{"sha256": digest, "trust": "authenticated-destination-unverified"},
	})
	var api smithy.APIError
	if !errors.As(err, &api) || api.ErrorCode() != "PreconditionFailed" {
		return err
	}
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return err
	}
	if out == nil || out.Metadata["sha256"] != digest {
		return errors.New("capture window occupied")
	}
	return nil
}

func allowed(ctx context.Context) bool {
	lc, ok := lambdacontext.FromContext(ctx)
	if !ok {
		return false
	}
	parts := strings.Split(lc.InvokedFunctionArn, ":")
	return len(parts) >= 7 && parts[0] == "arn" && parts[1] == "aws" &&
		parts[2] == "lambda" && parts[3] == "us-east-1" && parts[4] == "576394965493" &&
		parts[5] == "function" && parts[6] == "api-payment-prd-fn-capturar-rendicion-khipu-v1" &&
		parts[6] == os.Getenv("AWS_LAMBDA_FUNCTION_NAME") &&
		os.Getenv("APP_STAGE") == "prd"
}

func verifier(c captureConfig) (providers.RenditionCaptureVerifier, error) {
	v := providers.RenditionCaptureVerifier{}
	if c.CertificatePEM == "" {
		return v, nil
	}
	block, rest := pem.Decode([]byte(c.CertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return v, providers.ErrVerification
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return v, err
	}
	// El pin solo puede proceder de configuración confiable, nunca del cuerpo.
	v.Pinned = cert
	return v, nil
}

// Handler aísla la captura en PRD y lee su interruptor SecureString en cada aviso.
func Handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	result := handle(ctx, req)
	// Solo estado técnico; nunca cuerpo, destino, certificados ni errores del SDK.
	zap.NewExample().Info("PRD rendition bootstrap", zap.Int("statusCode", result.StatusCode))
	return result, nil
}

func handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if !allowed(ctx) {
		return response(503)
	}
	if req.RequestContext.HTTP.Method != "POST" {
		return response(405)
	}
	if len(req.Body) > providers.MaxRenditionBytes*4/3+8 {
		return response(413)
	}
	bucket := os.Getenv("RECEIPTS_BUCKET_NAME")
	if bucket == "" {
		return response(503)
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return response(503)
	}
	out, err := ssm.NewFromConfig(cfg).GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String("/indomito/prd/payments/khipu/renditions/bootstrap-config"), WithDecryption: aws.Bool(true),
	})
	if err != nil || out.Parameter == nil || out.Parameter.Type != ssmtypes.ParameterTypeSecureString || out.Parameter.Value == nil {
		return response(503)
	}
	var c captureConfig
	if json.Unmarshal([]byte(*out.Parameter.Value), &c) != nil {
		return response(503)
	}
	v, err := verifier(c)
	if err != nil {
		return response(503)
	}
	a := App{Store: objects{client: s3.NewFromConfig(cfg), bucket: bucket}, Verifier: v, Enabled: c.Enabled, Until: c.Until, Now: time.Now}
	return a.Handle(ctx, req)
}
