package renditioncapture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

type captureConfig struct {
	Bucket string    `json:"bucket"`
	Until  time.Time `json:"until"`
}

type objects struct {
	client *s3.Client
	bucket string
}

func (s objects) Digest(ctx context.Context, key string) (string, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	var api smithy.APIError
	if errors.As(err, &api) && (api.ErrorCode() == "NotFound" || api.ErrorCode() == "NoSuchKey") {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if out.Metadata["sha256"] == "" {
		return "", errors.New("missing capture digest")
	}
	return out.Metadata["sha256"], nil
}

func (s objects) Put(ctx context.Context, key, digest string, body []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: bytes.NewReader(body), IfNoneMatch: aws.String("*"), ContentType: aws.String("application/json"), Metadata: map[string]string{"sha256": digest, "trust": "unverified"}})
	var api smithy.APIError
	if errors.As(err, &api) && api.ErrorCode() == "PreconditionFailed" {
		return ErrOccupied
	}
	return err
}

// Handler impide ejecutar la captura fuera de la función/cuenta DEV autorizada.
// La ventana de captura y el bucket se leen de SSM SecureString en cada petición.
func Handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	lc, ok := lambdacontext.FromContext(ctx)
	if !ok || !strings.HasPrefix(lc.InvokedFunctionArn, "arn:aws:lambda:us-east-1:382670112717:function:ind-khipu-rendition-capture-dev") {
		return response(503), nil
	}
	if req.RequestContext.HTTP.Method != "POST" {
		return response(405), nil
	}
	if len(req.Body) > 350000 {
		return response(413), nil
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return response(503), nil
	}
	out, err := ssm.NewFromConfig(cfg).GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String("/indomito/dev/payments/khipu/renditions/capture-config"), WithDecryption: aws.Bool(true)})
	if err != nil || out.Parameter == nil || out.Parameter.Type != ssmtypes.ParameterTypeSecureString || out.Parameter.Value == nil {
		return response(503), nil
	}
	var c captureConfig
	if json.Unmarshal([]byte(*out.Parameter.Value), &c) != nil || c.Bucket != "ind-khipu-rendition-capture-dev-382670112717" {
		return response(503), nil
	}
	a := App{Store: objects{client: s3.NewFromConfig(cfg), bucket: c.Bucket}, Until: c.Until, Now: time.Now}
	return a.Handle(ctx, req), nil
}
